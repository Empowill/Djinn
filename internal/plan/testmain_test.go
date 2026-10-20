package plan_test

import (
	"os"
	"testing"

	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	removeFakes := plan.FakeClaudeMain()
	cleanup := testx.Guard()
	code := m.Run()
	cleanup()
	removeFakes()
	os.Exit(code)
}
