package config

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// maxSecret bounds a secret file: a key or a database address, never more.
const maxSecret = 64 << 10

// Secret is a secret the installer hands over: the variable key itself (servers set up
// before the secrets moved out of .env), else the file key_FILE names, as compose mounts
// it from /opt/mikan/secrets (/run/secrets/<name>), trimmed. "" when neither is set, or the
// file is empty.
func Secret(key string) (string, error) {
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	path := os.Getenv(key + "_FILE")
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE: %w", key, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxSecret+1))
	if err != nil {
		return "", fmt.Errorf("%s_FILE: %w", key, err)
	}
	if len(raw) > maxSecret {
		return "", fmt.Errorf("%s_FILE: larger than %d bytes", key, maxSecret)
	}
	return strings.TrimSpace(string(raw)), nil
}
