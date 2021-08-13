package field

import "io"

// ID is an event field that sets the event's ID.
// It takes a string as its value, given that IDs can be of any value.
//
// Use strconv.Itoa if you want to use integer IDs.
type ID struct {
	SingleLine

	ID string
}

func (i ID) name() string {
	return "id"
}

func (i ID) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write([]byte(i.ID))

	return int64(n), err
}
