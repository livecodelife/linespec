package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents the logging level
type Level int

const (
	// ErrorLevel only shows errors
	ErrorLevel Level = iota
	// InfoLevel shows info messages and errors (normal mode)
	InfoLevel
	// DebugLevel shows all messages including debug
	DebugLevel
)

var (
	globalLevel              = InfoLevel
	globalOutput   io.Writer = os.Stdout
	globalErrorOut io.Writer = os.Stderr
	mu             sync.RWMutex
)

// Spinner frames for animation
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerHandle represents a running spinner
type SpinnerHandle struct {
	stopChan chan bool
	doneChan chan bool
}

// SetLevel sets the global logging level
func SetLevel(level Level) {
	mu.Lock()
	defer mu.Unlock()
	globalLevel = level
}

// SetOutput sets the output writers
func SetOutput(stdout, stderr io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	globalOutput = stdout
	globalErrorOut = stderr
}

// IsDebug returns true if debug mode is enabled
func IsDebug() bool {
	mu.RLock()
	defer mu.RUnlock()
	return globalLevel >= DebugLevel
}

// Debug logs a debug message (only shown with --debug flag)
func Debug(format string, args ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	if globalLevel >= DebugLevel {
		fmt.Fprintf(globalOutput, "[DEBUG] "+format+"\n", args...)
	}
}

// Info logs an info message (shown in normal mode)
func Info(format string, args ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	if globalLevel >= InfoLevel {
		fmt.Fprintf(globalOutput, format+"\n", args...)
	}
}

// Error logs an error message (always shown)
func Error(format string, args ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	fmt.Fprintf(globalErrorOut, format+"\n", args...)
}

// TestRunning prints the test header without spinner
func TestRunning(current, total int, name string) {
	mu.RLock()
	defer mu.RUnlock()
	if globalLevel >= InfoLevel {
		fmt.Fprintf(globalOutput, "\n[%d/%d] %s\n", current, total, name)
	}
}

// TestPassed prints a simple success message
func TestPassed() {
	mu.RLock()
	defer mu.RUnlock()
	if globalLevel >= InfoLevel {
		fmt.Fprintf(globalOutput, "✅ Passed!\n")
	}
}

// TestFailed prints the test failure with details
func TestFailed(name string, err error) {
	mu.RLock()
	defer mu.RUnlock()
	fmt.Fprintf(globalErrorOut, "\n❌ Test %s FAILED:\n  %v\n", name, err)
}

// Summary logs the test summary
func Summary(total, passed, failed int) {
	mu.RLock()
	defer mu.RUnlock()
	fmt.Fprintf(globalOutput, "\n================ Summary ================\n")
	fmt.Fprintf(globalOutput, "Total:  %d\n", total)
	fmt.Fprintf(globalOutput, "Passed: %d\n", passed)
	fmt.Fprintf(globalErrorOut, "Failed: %d\n", failed)
}

// ShowSpinner displays an animated spinner with a message
// Returns a handle that can be used to stop the spinner
func ShowSpinner(message string) *SpinnerHandle {
	handle := &SpinnerHandle{
		stopChan: make(chan bool),
		doneChan: make(chan bool),
	}

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		defer close(handle.doneChan)

		frameIdx := 0
		for {
			select {
			case <-handle.stopChan:
				// Clear the spinner line
				fmt.Fprint(globalOutput, "\r\033[K")
				return
			case <-ticker.C:
				frame := spinnerFrames[frameIdx%len(spinnerFrames)]
				fmt.Fprintf(globalOutput, "\r%s %s", frame, message)
				frameIdx++
			}
		}
	}()

	return handle
}

// StopSpinner stops the spinner and waits for it to finish
func StopSpinner(handle *SpinnerHandle) {
	if handle != nil {
		close(handle.stopChan)
		<-handle.doneChan
	}
}

// SetupComplete prints the completion message after setup
func SetupComplete() {
	mu.RLock()
	defer mu.RUnlock()
	if globalLevel >= InfoLevel {
		// Clear the spinner line and print completion
		fmt.Fprint(globalOutput, "\r\033[K")
		fmt.Fprintf(globalOutput, "✅ Setup complete\n")
	}
}

// TestingMessage prints the "Testing..." message with spinner start
func TestingMessage() *SpinnerHandle {
	return ShowSpinner("Testing...")
}
