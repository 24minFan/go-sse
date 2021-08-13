package field

import "io"

// Comment is an event comment field. If it spans on multiple lines,
// new comment lines are created.
type Comment struct {
	Message string
}

func (c Comment) name() string {
	return ""
}

func (c Comment) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write([]byte(c.Message))

	return int64(n), err
}
