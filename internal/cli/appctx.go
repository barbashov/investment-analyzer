package cli

import "io"

type globalOpts struct {
	DBPath string
	From   string
	To     string
}

type appContext struct {
	opts  globalOpts
	stdin io.Reader
	out   io.Writer
	err   io.Writer
}
