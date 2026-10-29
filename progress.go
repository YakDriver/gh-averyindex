package main

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/term"
)

const (
	// spinnerInterval is how long each spinner frame shows.
	spinnerInterval = 120 * time.Millisecond

	// clearLine returns to the start of the line and erases it.
	clearLine = "\r\033[K"
)

var (
	// arcFrames is a thin arc orbiting.
	arcFrames = []string{"◜", "◝", "◞", "◟"}
	// asciiFrames is for terminals that may not render the arc.
	asciiFrames = []string{"|", "/", "-", `\`}
)

// spinnerFrames picks frames for the current terminal. TERM=dumb usually
// means a basic terminal or an editor's output pane.
func spinnerFrames() []string {
	if os.Getenv("TERM") == "dumb" {
		return asciiFrames
	}
	return arcFrames
}

// spinner animates a frame and label in place on one line so a long run
// doesn't look hung. It's also an io.Writer for notices: it clears its line
// before anything else is written, then redraws on the next frame.
type spinner struct {
	mu     sync.Mutex
	w      io.Writer
	frames []string
	label  string
	frame  int
	drawn  bool
	stop   chan struct{}
	done   chan struct{}
}

func newSpinner(w io.Writer, frames []string, label string) *spinner {
	return &spinner{w: w, frames: frames, label: label}
}

// Start animates until Stop is called.
func (s *spinner) Start(interval time.Duration) {
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.mu.Lock()
				io.WriteString(s.w, "\r"+s.frames[s.frame%len(s.frames)]+" "+s.label)
				s.frame++
				s.drawn = true
				s.mu.Unlock()
			}
		}
	}()
}

// Stop stops the animation and erases the spinner line. It's safe to call
// more than once.
func (s *spinner) Stop() {
	if s.stop != nil {
		close(s.stop)
		<-s.done
		s.stop = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
}

func (s *spinner) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
	return s.w.Write(b)
}

func (s *spinner) clear() {
	if s.drawn {
		io.WriteString(s.w, clearLine)
		s.drawn = false
	}
}

// isTerminal reports whether f is an interactive terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(f)
}

// colorEnabled reports whether stdout should get color, following gh's
// conventions (NO_COLOR, CLICOLOR_FORCE, GH_FORCE_TTY).
func colorEnabled() bool {
	return term.FromEnv().IsColorEnabled()
}
