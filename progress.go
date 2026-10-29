package main

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cli/go-gh/v2/pkg/term"
	xterm "golang.org/x/term"
)

const (
	// spinnerInterval is how long each spinner frame shows.
	spinnerInterval = 120 * time.Millisecond

	// defaultWidth is used when the terminal width can't be read.
	defaultWidth = 80
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

// spinner animates a frame and label in place on one row so a long run
// doesn't look hung. It's also an io.Writer for notices: it erases its row
// before anything else is written, then redraws on the next frame.
//
// It never wraps, because \r only returns to the start of the current row:
// the line is cut to fit the terminal. It erases by overwriting with spaces
// rather than an ANSI erase code, so it works on basic terminals too.
type spinner struct {
	mu     sync.Mutex
	w      io.Writer
	frames []string
	label  string
	width  int // terminal columns
	frame  int
	drawn  int // columns currently on screen
	stop   chan struct{}
	done   chan struct{}
}

func newSpinner(w io.Writer, frames []string, label string, width int) *spinner {
	if width <= 0 {
		width = defaultWidth
	}
	return &spinner{w: w, frames: frames, label: label, width: width}
}

// line is the frame and label, cut to leave the terminal's last column
// free. Writing into the last column makes some terminals wrap early.
func (s *spinner) line() string {
	l := []rune(s.frames[s.frame%len(s.frames)] + " " + s.label)
	if limit := s.width - 1; len(l) > limit {
		l = append(l[:max(0, limit-1)], '…')
	}
	return string(l)
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
				l := s.line()
				// Pad over any longer line left from before.
				n := utf8.RuneCountInString(l)
				io.WriteString(s.w, "\r"+l+strings.Repeat(" ", max(0, s.drawn-n)))
				s.frame++
				s.drawn = max(n, s.drawn)
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
	if s.drawn > 0 {
		io.WriteString(s.w, "\r"+strings.Repeat(" ", s.drawn)+"\r")
		s.drawn = 0
	}
}

// terminalWidth is f's width in columns, or 0 if it can't be read.
func terminalWidth(f *os.File) int {
	w, _, err := xterm.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return w
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
