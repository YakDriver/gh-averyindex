package main

import (
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// progressInterval is how often a dot is printed while collecting.
const progressInterval = 5 * time.Second

// progress prints a dot to w at a fixed interval so a long run doesn't look
// hung. It's also an io.Writer for notices: anything written through it
// starts on a fresh line instead of after the dots.
type progress struct {
	mu     sync.Mutex
	w      io.Writer
	dotted bool
	stop   chan struct{}
	done   chan struct{}
}

func newProgress(w io.Writer) *progress {
	return &progress{w: w}
}

// Start prints a dot every interval until Stop is called.
func (p *progress) Start(interval time.Duration) {
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(p.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				p.mu.Lock()
				io.WriteString(p.w, ".")
				p.dotted = true
				p.mu.Unlock()
			}
		}
	}()
}

// Stop stops the dots and ends their line.
func (p *progress) Stop() {
	if p.stop != nil {
		close(p.stop)
		<-p.done
		p.stop = nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLine()
}

func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLine()
	return p.w.Write(b)
}

func (p *progress) endLine() {
	if p.dotted {
		io.WriteString(p.w, "\n")
		p.dotted = false
	}
}

// isTerminal reports whether f is an interactive terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
