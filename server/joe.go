package server

import (
	"context"
	"fmt"
	"time"

	"github.com/tmaxmax/go-sse/server/event"
)

// A ReplayProvider is a type that can replay older published events to new subscribers.
// Replay providers use event IDs, the topics the events were published to and optionally
// the events' expiration times or any other criteria to determine which are valid for replay.
//
// While providers can require events to have IDs beforehand, they can also set the IDs themselves,
// automatically - it's up to the implementation.
//
// Replay providers are not required to be thread-safe - server providers are required to ensure only
// one operation is executed on the replay provider at any given time. Server providers may not execute
// replay operation concurrently with other operations, so make sure any action on the replay provider
// blocks for as little as possible. If a replay provider is thread-safe, some operations may be
// run in a separate goroutine - see the interface's method documentation.
//
// Executing actions that require waiting for a long time on I/O, such as HTTP requests or database
// calls must be handled with great care, so the server provider is not blocked. Reducing them to
// the minimum by using techniques such as caching or by executing them in separate goroutines is
// recommended, as long as the implementation fulfills the requirements.
//
// If not specified otherwise, the errors returned are implementation-specific.
type ReplayProvider interface {
	// Put adds a new event to the replay buffer. The message's event may be modified by
	// the provider, if it sets an ID.
	//
	// The Put operation may be executed by the replay provider in another goroutine only if
	// it can ensure that any Replay operation called after the Put goroutine is started
	// can replay the new received message. This also requires the replay provider implementation
	// to be thread-safe.
	//
	// Replay providers are not required to guarantee that after Put returns the new events
	// can be replayed. If an error occurs and retrying the operation would block for too
	// long, it can be aborted. The errors aren't returned as the server providers won't be able
	// to handle them in a useful manner anyway.
	Put(message *Message)
	// Replay sends to a new subscriber all the valid events received by the provider
	// since the event with the subscription's ID. If the ID the subscription provides
	// is invalid, the provider should not replay any events.
	//
	// Replay operations must be executed in the same goroutine as the one it is called in.
	// Other goroutines may be launched from inside the Replay method, but the events must
	// be sent to the subscription in the same goroutine that Replay is called in.
	Replay(subscription Subscription)
	// GC triggers a cleanup. After GC returns, all the events that are invalid according
	// to the provider's criteria should be impossible to replay again.
	//
	// If GC returns an error, the provider is not required to try to trigger another
	// GC ever again. Make sure that before you return a non-nil value you handle
	// temporary errors accordingly, with blocking as shortly as possible.
	// If your implementation does not require GC, return a non-nil error from this method.
	// This way server providers won't try to GC at all, improving their performance.
	//
	// If the replay provider implementation is thread-safe the GC operation can be executed in another goroutine.
	GC() error
}

// ReplayError is an error returned when a replay failed. It contains the ID for
// which the replay failed and optionally another error value that describes why
// the replay failed.
type ReplayError struct {
	err error
	id  event.ID
}

func (e *ReplayError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("server.replay.Provider: invalid ID %q: %s", e.id, e.err.Error())
	}
	return fmt.Sprintf("server.replay.Provider: ID %q does not exist", e.id)
}

type subscriber = chan<- *event.Event
type subscribers = map[subscriber]struct{}

// Joe is a basic server provider that synchronously executes operations by queueing them in channels.
// Events are also sent synchronously to subscribers, but Joe doesn't wait for the subscribers to have
// received the events - if a subscriber's channel is not ready to receive, it skips that subscriber.
// You can configure Joe to also wait for a fixed duration before skipping.
//
// Joe supports event replaying with the help of a replay provider. As operations are executed
// synchronously, it is guaranteed that no new events will be omitted from sending to a new subscriber
// because older events are still replaying when the event is sent to Joe.
//
// If due to some unexpected scenario (the replay provider has a bug, for example) a panic occurs,
// Joe will close all the subscribers' channels, so requests aren't closed abruptly.
//
// He serves simple use-cases well, as he's light on resources, and does not require any external
// services. Also, he is the default provider for Servers.
type Joe struct {
	message        chan Message
	subscription   chan Subscription
	unsubscription chan subscriber
	done           chan struct{}
	gc             <-chan time.Time
	stopGC         func()
	send           sendFunction
	topics         map[string]subscribers
	subscribers    subscribers
	replay         ReplayProvider
}

// JoeConfig is used to tune Joe to preference.
type JoeConfig struct {
	// Joe receives published events on a dedicated channel. If the publisher's goroutine is blocked
	// because Joe can't keep up with the load, use a bigger buffer. This shouldn't be a concern
	// and if it is other providers might be suited better for your use-case.
	//
	// The buffer size defaults to 1.
	MessageChannelBuffer int
	// An optional replay provider that Joe uses to resend older messages to new subscribers.
	ReplayProvider ReplayProvider
	// An optional interval at which Joe triggers a cleanup of expired messages, if the replay provider supports it.
	// See the desired provider's documentation to determine if periodic cleanup is necessary.
	ReplayGCInterval time.Duration
	// An optional value that represents the duration that Joe will wait for an event to be received by a connection.
	// It is 0 by default. This shouldn't be a concern and if it is other providers might be suited better for your
	// use-case.
	SendTimeout time.Duration
}

// NewJoe creates and starts a Joe.
func NewJoe(configuration ...JoeConfig) *Joe {
	config := joeConfig(configuration)

	gc, stopGCTicker := ticker(config.ReplayGCInterval)
	send, stopSendTimer := sendFn(config.SendTimeout)

	j := &Joe{
		message:        make(chan Message, config.MessageChannelBuffer),
		subscription:   make(chan Subscription),
		unsubscription: make(chan subscriber),
		done:           make(chan struct{}),
		gc:             gc,
		stopGC:         stopGCTicker,
		send:           send,
		topics:         map[string]subscribers{},
		subscribers:    subscribers{},
		replay:         config.ReplayProvider,
	}

	go func() {
		defer stopGCTicker()
		defer stopSendTimer()

		j.start()
	}()

	return j
}

func (j *Joe) Subscribe(ctx context.Context, sub Subscription) error {
	sub.Topics = topics(sub.Topics)

	go func() {
		// We are also waiting on done here so if Joe is stopped but not the HTTP server that
		// serves the request this goroutine isn't hanging.
		select {
		case <-ctx.Done():
		case <-j.done:
			return
		}

		// We are waiting on done here so the goroutine isn't blocked if Joe is stopped when
		// this point is reached.
		select {
		case j.unsubscription <- sub.Channel:
		case <-j.done:
		}
	}()

	// Waiting on done ensures Subscribe behaves as required by the Provider interface
	// if Stop was called. It also ensures Subscribe doesn't block if a new request arrives
	// after Joe is stopped, which would otherwise result in a client waiting forever.
	select {
	case j.subscription <- sub:
		return nil
	case <-j.done:
		return ErrProviderClosed
	}
}

func (j *Joe) Publish(msg Message) error {
	// Waiting on done ensures Publish doesn't block the caller goroutine
	// when Joe is stopped and implements the required Provider behavior.
	select {
	case j.message <- msg:
		return nil
	case <-j.done:
		return ErrProviderClosed
	}
}

func (j *Joe) Stop() error {
	// Waiting on Stop here prevents double-closing and implements the required Provider behavior.
	select {
	case <-j.done:
		return ErrProviderClosed
	default:
		close(j.done)
		return nil
	}
}

func (j *Joe) topic(identifier string) subscribers {
	if _, ok := j.topics[identifier]; !ok {
		j.topics[identifier] = subscribers{}
	}
	return j.topics[identifier]
}

func (j *Joe) start() {
	// defer closing all subscribers instead of closing them when done is closed
	// so in case of a panic subscribers won't block the request goroutines forever.
	defer j.closeSubscribers()

	for {
		select {
		case msg := <-j.message:
			j.replay.Put(&msg)

			for sub := range j.topics[msg.Topic] {
				j.send(sub, msg.Event)
			}
		case sub := <-j.subscription:
			if _, ok := j.subscribers[sub.Channel]; ok {
				continue
			}

			j.replay.Replay(sub)

			for _, topic := range sub.Topics {
				j.topic(topic)[sub.Channel] = struct{}{}
			}
			j.subscribers[sub.Channel] = struct{}{}
		case unsub := <-j.unsubscription:
			for _, subs := range j.topics {
				delete(subs, unsub)
			}

			delete(j.subscribers, unsub)
			close(unsub)
		case <-j.gc:
			if err := j.replay.GC(); err != nil {
				j.stopGC()
			}
		case <-j.done:
			return
		}
	}
}

func (j *Joe) closeSubscribers() {
	for sub := range j.subscribers {
		close(sub)
	}
}

var _ Provider = (*Joe)(nil)