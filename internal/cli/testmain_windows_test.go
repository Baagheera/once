//go:build windows

package cli

import (
	"os"
	"testing"

	"github.com/Baagheera/once/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithPrivateTemp(m.Run))
}
