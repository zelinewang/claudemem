package cmd

import (
	"errors"
	"reflect"
	"testing"
)

type fakeSyncStatusProvider struct {
	initialized bool
	remote      string
	status      string
	err         error
}

func (f fakeSyncStatusProvider) IsInitialized() bool { return f.initialized }
func (f fakeSyncStatusProvider) RemoteURL() string   { return f.remote }
func (f fakeSyncStatusProvider) Status() (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.status, nil
}

func TestNewSyncStatusPayload_Uninitialized(t *testing.T) {
	got, err := newSyncStatusPayload(fakeSyncStatusProvider{}, "/tmp/store")
	if err != nil {
		t.Fatalf("newSyncStatusPayload: %v", err)
	}
	if got.Initialized {
		t.Fatal("expected initialized=false")
	}
	if got.Status != "not initialized" {
		t.Fatalf("status = %q, want not initialized", got.Status)
	}
	if got.Clean {
		t.Fatal("uninitialized store should not report clean")
	}
	if len(got.StatusLines) != 0 {
		t.Fatalf("status lines = %#v, want empty", got.StatusLines)
	}
}

func TestNewSyncStatusPayload_CleanRepo(t *testing.T) {
	got, err := newSyncStatusPayload(fakeSyncStatusProvider{
		initialized: true,
		remote:      "git@example.com:memory.git",
		status:      "",
	}, "/tmp/store")
	if err != nil {
		t.Fatalf("newSyncStatusPayload: %v", err)
	}
	if !got.Clean {
		t.Fatal("expected clean=true")
	}
	if got.Remote != "git@example.com:memory.git" {
		t.Fatalf("remote = %q", got.Remote)
	}
	if len(got.StatusLines) != 0 {
		t.Fatalf("status lines = %#v, want empty", got.StatusLines)
	}
}

func TestNewSyncStatusPayload_DirtyRepo(t *testing.T) {
	got, err := newSyncStatusPayload(fakeSyncStatusProvider{
		initialized: true,
		status:      "?? notes/project/new.md\n M notes/reference/existing.md\n",
	}, "/tmp/store")
	if err != nil {
		t.Fatalf("newSyncStatusPayload: %v", err)
	}
	if got.Clean {
		t.Fatal("expected clean=false")
	}
	if got.Remote != "(none)" {
		t.Fatalf("remote = %q, want (none)", got.Remote)
	}
	want := []string{"?? notes/project/new.md", "M notes/reference/existing.md"}
	if !reflect.DeepEqual(got.StatusLines, want) {
		t.Fatalf("status lines = %#v, want %#v", got.StatusLines, want)
	}
}

func TestNewSyncStatusPayload_StatusError(t *testing.T) {
	_, err := newSyncStatusPayload(fakeSyncStatusProvider{
		initialized: true,
		err:         errors.New("git failed"),
	}, "/tmp/store")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSyncCommandErrorQuietStillFails(t *testing.T) {
	baseErr := errors.New("git failed")
	err := syncCommandError("pull", true, baseErr)
	if err == nil {
		t.Fatal("quiet sync error should still fail")
	}
	if !errors.Is(err, baseErr) {
		t.Fatalf("quiet sync error should wrap original error, got %v", err)
	}
}

func TestSyncCommandErrorNonQuietReturnsOriginal(t *testing.T) {
	baseErr := errors.New("git failed")
	err := syncCommandError("push", false, baseErr)
	if !errors.Is(err, baseErr) {
		t.Fatalf("non-quiet sync error should return original error, got %v", err)
	}
}
