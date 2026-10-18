package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// What stops djinn up, named in the log's "shutdown begins" line (decision Q84). An install names itself
// (ui.StopBy), behind restartStop.
const (
	stopCtrlQ   = "Ctrl+Q (Cmd+Q on macOS) in the window"
	stopTray    = "Quit Djinn in the tray"
	stopSIGINT  = "SIGINT (Ctrl+C in djinn up's terminal, or kill -INT)"
	stopSIGTERM = "SIGTERM (kill, systemd, or the session ending)"
	stopWindow  = "the window ended"
	restartStop = "restart: "
)

// stopError is what stopped djinn up, the cause of its context: not a failure.
type stopError string

func (c stopError) Error() string { return string(c) }

// stopContext is djinn up's context: done at SIGINT or SIGTERM, or once stop is called, for a source. The first
// source is the context's cause, and stopWhy says it. release lets the signals go, as signal.NotifyContext's stop.
func stopContext(parent context.Context, signals <-chan os.Signal) (ctx context.Context, stop func(source string), release func()) {
	ctx, cancel := context.WithCancelCause(parent)
	stop = func(source string) { cancel(stopError(source)) }
	var ch chan os.Signal
	if signals == nil {
		ch = make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		signals = ch
	}
	go func() {
		select {
		case s := <-signals:
			stop(signalSource(s))
		case <-ctx.Done():
		}
	}()
	return ctx, stop, func() {
		if ch != nil {
			signal.Stop(ch)
		}
		cancel(context.Canceled)
	}
}

// signalSource names the signal that stops djinn up.
func signalSource(s os.Signal) string {
	switch s {
	case os.Interrupt:
		return stopSIGINT
	case syscall.SIGTERM:
		return stopSIGTERM
	}
	return s.String()
}

// stopWhy is what stopped djinn up, from the cause of its context; "unknown" when none was named.
func stopWhy(ctx context.Context) string {
	var c stopError
	if errors.As(context.Cause(ctx), &c) {
		return string(c)
	}
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause.Error()
	}
	return "unknown"
}
