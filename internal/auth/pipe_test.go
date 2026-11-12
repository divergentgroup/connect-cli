package auth

import "io"

func newPipe() (io.Reader, io.Writer) { return io.Pipe() }
