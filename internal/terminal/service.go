package terminal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	terminalv1 "github.com/empowill/djinn/gen/go/terminal/v1"
	"github.com/empowill/djinn/gen/go/terminal/v1/terminalv1connect"
	"github.com/empowill/djinn/internal/plan"
)

// Handler returns the Connect handler of TerminalService on m, and its path prefix.
func Handler(m *Manager) (string, http.Handler) {
	return terminalv1connect.NewTerminalServiceHandler(&Service{m: m}, connect.WithInterceptors(plan.Validate))
}

// Service implements TerminalService.
type Service struct {
	terminalv1connect.UnimplementedTerminalServiceHandler
	m *Manager
}

func (s *Service) Open(
	_ context.Context, req *connect.Request[terminalv1.TerminalServiceOpenRequest],
) (*connect.Response[terminalv1.TerminalServiceOpenResponse], error) {
	msg := req.Msg
	command := msg.GetCommand()
	if msg.GetLine() != "" {
		if len(command) > 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("set a command or a line, not both"))
		}
		command = ShellCommand(msg.GetLine())
	}
	if len(command) == 0 && strings.HasPrefix(strings.ToLower(msg.GetName()), "lead-") {
		// A lead's terminal starts with its lead (djinn wish resume), never with a shell a text told would run in.
		t := s.m.Lookup(msg.GetName())
		if t == nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s runs no lead: resume the wish to start it", msg.GetName()))
		}
		if msg.GetCols() > 0 && msg.GetRows() > 0 {
			_ = t.Resize(int(msg.GetCols()), int(msg.GetRows())) // Ended meanwhile: the window reads it.
		}
		return connect.NewResponse(&terminalv1.TerminalServiceOpenResponse{Terminal: describe(t), Attached: true}), nil
	}
	t, attached, err := s.m.Open(msg.GetName(), command, msg.GetDirectory(), int(msg.GetCols()), int(msg.GetRows()))
	if err != nil {
		return nil, status(err, connect.CodeInvalidArgument)
	}
	if attached && msg.GetCols() > 0 && msg.GetRows() > 0 {
		_ = t.Resize(int(msg.GetCols()), int(msg.GetRows())) // Ended meanwhile: the window reads it.
	}
	return connect.NewResponse(&terminalv1.TerminalServiceOpenResponse{Terminal: describe(t), Attached: attached}), nil
}

func (s *Service) Write(
	_ context.Context, req *connect.Request[terminalv1.TerminalServiceWriteRequest],
) (*connect.Response[terminalv1.TerminalServiceWriteResponse], error) {
	t, err := s.m.Get(req.Msg.GetId())
	if err == nil {
		err = t.Write(req.Msg.GetData())
	}
	if err != nil {
		return nil, status(err, connect.CodeInternal)
	}
	return connect.NewResponse(&terminalv1.TerminalServiceWriteResponse{}), nil
}

func (s *Service) Resize(
	_ context.Context, req *connect.Request[terminalv1.TerminalServiceResizeRequest],
) (*connect.Response[terminalv1.TerminalServiceResizeResponse], error) {
	t, err := s.m.Get(req.Msg.GetId())
	if err == nil {
		err = t.Resize(int(req.Msg.GetCols()), int(req.Msg.GetRows()))
	}
	if err != nil {
		return nil, status(err, connect.CodeInternal)
	}
	return connect.NewResponse(&terminalv1.TerminalServiceResizeResponse{}), nil
}

func (s *Service) Read(
	ctx context.Context, req *connect.Request[terminalv1.TerminalServiceReadRequest],
	stream *connect.ServerStream[terminalv1.TerminalServiceReadResponse],
) error {
	t, err := s.m.Get(req.Msg.GetId())
	if err != nil {
		return status(err, connect.CodeInternal)
	}
	return t.Read(req.Msg.GetFromOffset(), ctx.Done(), func(o Output) error {
		return stream.Send(&terminalv1.TerminalServiceReadResponse{
			Offset: o.Offset, Data: o.Data, Exited: o.Exited, ExitCode: int32(o.Code),
		})
	})
}

func (s *Service) Close(
	_ context.Context, req *connect.Request[terminalv1.TerminalServiceCloseRequest],
) (*connect.Response[terminalv1.TerminalServiceCloseResponse], error) {
	t, err := s.m.Get(req.Msg.GetId())
	if err != nil {
		return nil, status(err, connect.CodeInternal)
	}
	t.Hangup()
	return connect.NewResponse(&terminalv1.TerminalServiceCloseResponse{}), nil
}

func describe(t *Terminal) *terminalv1.Terminal {
	cols, rows, exited, code := t.State()
	return &terminalv1.Terminal{
		Id: t.ID, Name: t.Name, Command: t.Command, Directory: t.Dir,
		Cols: uint32(cols), Rows: uint32(rows), Exited: exited, ExitCode: int32(code),
	}
}

// status gives err its Connect code; other errors get code.
func status(err error, code connect.Code) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrExited), errors.Is(err, ErrClosed), errors.Is(err, ErrBusy):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(code, err)
}
