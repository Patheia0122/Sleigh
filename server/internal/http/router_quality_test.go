package http

import (
	"strings"
	"testing"
)

func TestPythonQualityCommandChangedFiles(t *testing.T) {
	cmd := pythonQualityCommand([]string{"covid_analysis.py"})
	if want := "python -m py_compile"; !strings.Contains(cmd, want) {
		t.Fatalf("got %q, want substring %q", cmd, want)
	}
	if strings.Contains(cmd, "compileall") {
		t.Fatalf("should not fall back to compileall for .py files: %q", cmd)
	}
}

func TestPythonQualityCommandNonPyFallsBackToCompileall(t *testing.T) {
	cmd := pythonQualityCommand([]string{"README.md"})
	if want := "compileall"; !strings.Contains(cmd, want) {
		t.Fatalf("got %q", cmd)
	}
}

func TestGoQualityCommandChangedFiles(t *testing.T) {
	cmd := goQualityCommand([]string{"main.go"})
	if !strings.Contains(cmd, "gofmt -l") || !strings.Contains(cmd, "main.go") {
		t.Fatalf("got %q", cmd)
	}
}
