package scripts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAntigravityConfigHonorsMCPCWD(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required to exercise the MCP config installer")
	}
	cases := []struct {
		name     string
		cwd      string
		wantArgs []string
		wantCWD  string
	}{
		{
			name:     "without cwd uses workspace argument",
			wantArgs: []string{"serve", "${workspaceFolder}"},
		},
		{
			name:     "with cwd omits literal workspace argument",
			cwd:      "/tmp/goray-workspace",
			wantArgs: []string{"serve"},
			wantCWD:  "/tmp/goray-workspace",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "mcp_config.json")
			//nolint:gosec // python3 is resolved from PATH and the script is repository tooling.
			cmd := exec.Command(python, "mcp-config.py", "install",
				"--format", "antigravity",
				"--config", config,
				"--name", "goray",
				"--command", "/usr/local/bin/goray",
				"--args", "serve",
			)
			cmd.Env = envWith(os.Environ(), "MCP_CWD", tc.cwd)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("mcp-config.py: %v\n%s", err, output)
			}

			data, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Servers map[string]struct {
					Args []string `json:"args"`
					CWD  string   `json:"cwd"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			entry, ok := decoded.Servers["goray"]
			if !ok {
				t.Fatalf("missing goray entry:\n%s", data)
			}
			if !slices.Equal(entry.Args, tc.wantArgs) {
				t.Fatalf("args = %v, want %v", entry.Args, tc.wantArgs)
			}
			if entry.CWD != tc.wantCWD {
				t.Fatalf("cwd = %q, want %q", entry.CWD, tc.wantCWD)
			}
		})
	}
}

func envWith(base []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}
