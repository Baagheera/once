//go:build windows

package oncehttp

import (
	"os"
	"testing"

	"github.com/Baagheera/once/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithPrivateTemp(m.Run))
}
