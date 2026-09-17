package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serve(t *testing.T, run RunFunc) net.Conn {
	t.Helper()
	// A Unix socket path is short by platform limit, so the workspace is not
	// named after the test.
	dir, err := os.MkdirTemp("", "brp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "worker.sock")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		close(ready)
		err := Serve(ctx, socket, Initialize{Protocol: 1, MinimumProtocol: 1, Bot: "repo-bot", Version: "dev", Capabilities: []string{"run", "progress", "repo-inventory", "branch-health"}}, run, nil)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("serve: %v", err)
		}
	}()
	<-ready
	for attempt := 0; attempt < 200; attempt++ {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			t.Cleanup(func() { conn.Close() })
			return conn
		}
	}
	t.Fatal("worker socket never opened")
	return nil
}

func post(t *testing.T, conn net.Conn, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://worker/v1/runs", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil }, DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

const runRequest = `{"protocol":1,"remote":"https://example.invalid/a.git","branch":"main","directory":"/tmp/a","state_directory":"/tmp/s","repo":"a/b","host":"example.invalid","agent":{"command":["fake"]},"since_head":"c0ffee","commits":["deadbeef"]}`

func TestServeReportsInventoryAndHealth(t *testing.T) {
	var seen Request
	conn := serve(t, func(_ context.Context, request Request, progress func(Progress)) (Result, error) {
		seen = request
		progress(Progress{Phase: "inventorying", Task: "Reading a/b"})
		return Result{
			Inventory: &Inventory{Branch: "main", DefaultBranch: "main", Head: "abc", Issues: []Issue{}, Pulls: []Pull{}, Releases: []Release{}},
			Health:    &BranchHealth{State: "repaired", Head: "abc", Failing: []string{"build"}, Pushed: "def"},
		}, nil
	})
	response := post(t, conn, runRequest)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var kinds []string
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, event.Type)
		if event.Type == "result" {
			if event.Result.Inventory == nil || event.Result.Inventory.Head != "abc" {
				t.Fatalf("inventory missing from %+v", event.Result)
			}
			if event.Result.Health == nil || event.Result.Health.Pushed != "def" {
				t.Fatalf("health missing from %+v", event.Result)
			}
		}
	}
	if strings.Join(kinds, ",") != "progress,result,complete" {
		t.Fatalf("events = %v", kinds)
	}
	if seen.SinceHead != "c0ffee" || len(seen.Commits) != 1 || seen.Commits[0] != "deadbeef" {
		t.Fatalf("Town's inventory inputs did not reach the worker: %+v", seen)
	}
}

func TestServeRejectsUnknownRequestFields(t *testing.T) {
	conn := serve(t, func(context.Context, Request, func(Progress)) (Result, error) {
		t.Fatal("a malformed request must not start work")
		return Result{}, nil
	})
	response := post(t, conn, `{"protocol":1,"branch":"main","surprise":true}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestServeReportsAFailedRunAsError(t *testing.T) {
	conn := serve(t, func(context.Context, Request, func(Progress)) (Result, error) {
		return Result{}, errors.New("read repository inventory: gh failed")
	})
	response := post(t, conn, runRequest)
	scanner := bufio.NewScanner(response.Body)
	var last Event
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &last); err != nil {
			t.Fatal(err)
		}
	}
	if last.Type != "error" || !strings.Contains(last.Error, "gh failed") {
		t.Fatalf("terminal event = %+v", last)
	}
}
