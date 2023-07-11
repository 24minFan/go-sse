package sse

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tmaxmax/go-sse/internal/parser"
)

func TestNew(t *testing.T) {
	t.Parallel()

	e := Message{Type: Type("x"), ID: ID("lol"), Retry: time.Second}
	e.AppendData("whatever", "input", "will\nbe\nchunked", "amazing")

	now := time.Now()
	e.ExpiresAt = now

	expected := Message{
		ExpiresAt: now,
		chunks: []chunk{
			{content: "whatever"},
			{content: "input"},
			{content: "will"},
			{content: "be"},
			{content: "chunked"},
			{content: "amazing"},
		},
		Retry: time.Second,
		Type:  Type("x"),
		ID:    ID("lol"),
	}

	require.Equal(t, expected, e, "invalid event")
}

func TestEvent_WriteTo(t *testing.T) {
	t.Parallel()

	e := Message{Type: Type("test_event"), ID: ID("example_id"), Retry: time.Second * 5}
	e.AppendData("This is an example\nOf an event", "", "a string here")
	e.Comment("This test should pass")
	e.AppendData("Important data\nImportant again\r\rVery important\r\n")

	output := "id: example_id\nevent: test_event\nretry: 5000\ndata: This is an example\ndata: Of an event\ndata: a string here\n: This test should pass\ndata: Important data\ndata: Important again\ndata: \ndata: Very important\n\n"
	expectedWritten := int64(len(output))

	w := &strings.Builder{}

	written, _ := e.WriteTo(w)

	require.Equal(t, output, w.String(), "event written incorrectly")
	require.Equal(t, expectedWritten, written, "written byte count wrong")
}

func TestEvent_UnmarshalText(t *testing.T) {
	t.Parallel()

	type test struct {
		name        string
		input       string
		expectedErr error
		expected    Message
	}

	nilEvent := Message{}
	nilEvent.reset()

	tests := []test{
		{
			name:        "No input",
			expected:    nilEvent,
			expectedErr: &UnmarshalError{Reason: ErrUnexpectedEOF},
		},
		{
			name:     "Invalid retry field",
			input:    "retry: sigma male\n",
			expected: nilEvent,
			expectedErr: &UnmarshalError{
				FieldName:  string(parser.FieldNameRetry),
				FieldValue: "sigma male",
				Reason:     fmt.Errorf("contains character %q, which is not an ASCII digit", 's'),
			},
		},
		{
			name:        "Valid input, no final newline",
			input:       "data: first\ndata:second\ndata:third",
			expected:    nilEvent,
			expectedErr: &UnmarshalError{Reason: ErrUnexpectedEOF},
		},
		{
			name:  "Valid input",
			input: "data: raw bytes here\nretry: 500\nretry: 1000\nid: 1000\nid: 2000\nid: \x001\n: with comments\ndata: again raw bytes\ndata: from multiple lines\nevent: overwritten name\nevent: my name here\n\ndata: I should be ignored",
			expected: Message{
				chunks: []chunk{
					{content: "raw bytes here"},
					{content: "with comments", isComment: true},
					{content: "again raw bytes"},
					{content: "from multiple lines"},
				},
				Retry: time.Second,
				Type:  Type("my name here"),
				ID:    ID("2000"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := Message{}

			if err := e.UnmarshalText([]byte(test.input)); (test.expectedErr != nil && err.Error() != test.expectedErr.Error()) || (test.expectedErr == nil && err != nil) {
				t.Fatalf("Invalid unmarshal error: got %q, want %q", err, test.expectedErr)
			}
			require.Equal(t, test.expected, e, "invalid unmarshal")
		})
	}
}

func newBenchmarkEvent() *Message {
	e := Message{Type: Type("This is the event's name"), ID: ID("example_id"), Retry: time.Minute}
	e.AppendData("Example data\nWith multiple rows\r\nThis is interesting")
	e.Comment("An useless comment here that spans\non\n\nmultiple\nlines")
	return &e
}

var benchmarkEvent = newBenchmarkEvent()

func BenchmarkEvent_WriteTo(b *testing.B) {
	b.ReportAllocs()

	for n := 0; n < b.N; n++ {
		_, _ = benchmarkEvent.WriteTo(io.Discard)
	}
}

var benchmarkText = []string{
	"Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
	"Pellentesque at dui non quam faucibus ultricies.",
	"Quisque non sem gravida, sodales lorem eget, lobortis est.",
	"Quisque porttitor nunc eu mollis congue.",
	"Vivamus sollicitudin tellus ut mi malesuada lacinia.",
	"Aenean aliquet tortor non urna sodales dignissim.",
	"Sed quis diam sed dui feugiat aliquam.",
	"Etiam sit amet neque cursus, semper nibh non, ornare nunc.",
	"Phasellus dignissim lacus vitae felis interdum, eget pharetra augue bibendum.",
	"Sed euismod enim sed ante laoreet, non ullamcorper enim dapibus.",
	"Ut accumsan arcu venenatis, egestas nisi consectetur, dignissim felis.",
	"Praesent lacinia elit ut tristique molestie.",
	"Mauris ut nibh id ante ultricies egestas.",
	"Mauris porttitor augue quis maximus efficitur.",
	"Fusce auctor enim viverra elit imperdiet, non dignissim dolor condimentum.",
	"Fusce scelerisque quam vel erat tempor elementum.",
	"Nullam ac velit in nisl hendrerit rhoncus sed ut dui.",
	"Pellentesque laoreet arcu vitae commodo gravida.",
	"Pellentesque sagittis enim quis sapien mollis tempor.",
	"Phasellus fermentum leo vitae odio efficitur, eu lacinia enim elementum.",
	"Morbi faucibus nisi a velit dictum eleifend.",
}

func BenchmarkEvent_WriteTo_text(b *testing.B) {
	ev := Message{}
	ev.AppendData(benchmarkText...)

	for n := 0; n < b.N; n++ {
		_, _ = ev.WriteTo(io.Discard)
	}
}
