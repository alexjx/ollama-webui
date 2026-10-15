package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Run in the built image as UID 1000; development hosts need not have Python/uv.
func TestContainerPythonRuntime(t *testing.T) {
	if os.Getenv("TEST_CONTAINER_PYTHON") != "1" {
		t.Skip("requires built container runtime")
	}
	workspace := t.TempDir()
	executor := ShellExecutor{Workspace: workspace, Timeout: 60 * time.Second, MaxOutput: 8192}
	command := `set -eu
uv --version
python3 --version
uv venv --offline --python /usr/bin/python3 uv-env
uv run --offline --no-project --python uv-env/bin/python python -c 'import json,pathlib; pathlib.Path("result.json").write_text(json.dumps({"answer": 42})); print("uv-ok")'
python3 -m venv standard-env
standard-env/bin/python -m pip --version
standard-env/bin/python -c 'import json,pathlib; assert json.loads(pathlib.Path("result.json").read_text())["answer"] == 42; print("venv-ok")'
# Install a tiny local package offline through both package managers.
mkdir -p wheel/example-1.0.dist-info
printf 'VALUE = 42\n' > wheel/example.py
printf 'Metadata-Version: 2.1\nName: example\nVersion: 1.0\n' > wheel/example-1.0.dist-info/METADATA
printf 'Wheel-Version: 1.0\nGenerator: smoke\nRoot-Is-Purelib: true\nTag: py3-none-any\n' > wheel/example-1.0.dist-info/WHEEL
printf '' > wheel/example-1.0.dist-info/RECORD
standard-env/bin/python -c 'import pathlib,zipfile; z=zipfile.ZipFile("example-1.0-py3-none-any.whl","w"); [z.write(p,p.relative_to("wheel")) for p in pathlib.Path("wheel").rglob("*") if p.is_file()]; z.close()'
uv pip install --offline --python uv-env/bin/python example-1.0-py3-none-any.whl
standard-env/bin/python -m pip install --no-index example-1.0-py3-none-any.whl
uv run --offline --no-project --python uv-env/bin/python python -c 'import example; assert example.VALUE == 42'
standard-env/bin/python -c 'import example; assert example.VALUE == 42'
# Even a normally writable /tmp stays outside the agent write boundary.
if standard-env/bin/python -c 'import pathlib; pathlib.Path("/tmp/python-sandbox-escape").write_text("bad")'; then
 exit 1
fi
printf 'packages-and-sandbox-ok\n'
`
	result := executor.Run(context.Background(), command, 0)
	if result.ExitCode != 0 || !strings.Contains(result.Output, "packages-and-sandbox-ok") {
		t.Fatalf("Python runtime failed: %#v", result)
	}
	t.Log(result.Output)
}
