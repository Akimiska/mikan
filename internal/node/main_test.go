package node

import (
	"os"
	"testing"
)

// The core's general settings go in before any test makes outbounds: the core writes its
// log level then, and the finalizers of outbounds other tests drop read it.
func TestMain(m *testing.M) {
	if err := applyBase(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
