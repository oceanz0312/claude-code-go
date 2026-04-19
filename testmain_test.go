package claudecodego

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var (
	fakeClaudePath  string
	redSquareImage  string
	shapesDemoImage string
	receiptDemoImage string
)

func TestMain(m *testing.M) {
	buildDir, err := os.MkdirTemp("", "claude-code-go-fakecli-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(buildDir)

	cacheDir, err := os.MkdirTemp("", "claude-code-go-gocache-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(cacheDir)

	tmpDir, err := os.MkdirTemp("", "claude-code-go-gotmp-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	fakeClaudePath = filepath.Join(buildDir, "fake-claude")
	cmd := exec.Command("go", "build", "-o", fakeClaudePath, "./testdata/fakeclaude")
	cmd.Env = append(os.Environ(), "GOCACHE="+cacheDir, "GOTMPDIR="+tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		panic(string(output))
	}

	redSquareImage = filepath.Join("testdata", "images", "red-square.png")
	shapesDemoImage = filepath.Join("testdata", "images", "shapes-demo.png")
	receiptDemoImage = filepath.Join("testdata", "images", "receipt-demo.png")

	os.Exit(m.Run())
}
