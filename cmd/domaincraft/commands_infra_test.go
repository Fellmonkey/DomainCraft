package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/pkg/logger"
)

func TestBuildStampsOnlyExactTags(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Contains(body, "describe --tags --abbrev=0") {
		t.Fatal("Makefile still stamps the nearest tag onto an untagged commit")
	}
	if !strings.Contains(body, "describe --tags --exact-match") {
		t.Fatal("Makefile does not use an exact tag match")
	}
}

func TestToolVersionsLiveInMakefile(t *testing.T) {
	ci, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(ci)
	if strings.Contains(body, "golangci-lint-action@") || strings.Contains(body, "govulncheck@v") {
		t.Fatal("CI still duplicates tool versions instead of calling the Makefile targets")
	}
}

func TestReleaseUsesSameTestGate(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go test -race -count=1 -shuffle=on ./...") {
		t.Fatal("release workflow does not use the CI test flags")
	}
}

func TestMainPackageHasSingleDocComment(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "// Package main") {
		t.Fatal("main.go repeats the package doc comment from commands.go")
	}
}

func TestInvalidBridgeRefIsUsageError(t *testing.T) {
	err := usageOnInvalidBridgeRef(bridge.ErrInvalidRef)
	if !errors.Is(err, errUsage) {
		t.Fatalf("invalid bridge ref exit code = runtime error, want usage error: %v", err)
	}
	runtimeErr := errors.New("clone failed")
	if got := usageOnInvalidBridgeRef(runtimeErr); !errors.Is(got, runtimeErr) || errors.Is(got, errUsage) {
		t.Fatalf("runtime error was remapped to a usage error: %v", got)
	}
}

func TestApplyAddonsWarnsOnUnknownName(t *testing.T) {
	var stderr bytes.Buffer
	log := logger.New()
	log.SetWriter(&stderr)
	project := &ir.Project{}
	applyAddons(&cliOptions{addons: "dapr, daprx"}, project, log)
	if !project.Addons["dapr"] {
		t.Fatal("known addon was not applied")
	}
	if !strings.Contains(stderr.String(), "unknown addon \"daprx\"") {
		t.Fatalf("unknown addon produced no warning: %q", stderr.String())
	}
}
