package sourcestore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveOpenPurge(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	archive := []byte("pretend tar.gz bytes")

	if err := s.Save("app-1", "build-1", archive); err != nil {
		t.Fatal(err)
	}
	f, size, err := s.Open("app-1", "build-1")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != string(archive) || size != int64(len(archive)) {
		t.Fatalf("got %q (%d bytes)", got, size)
	}
	if _, err := os.Stat(filepath.Join(root, "app-1", "build-1.tar.gz.part")); !os.IsNotExist(err) {
		t.Fatal("the temporary file must not be left behind")
	}

	if err := s.PurgeApplication(context.Background(), "app-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open("app-1", "build-1"); err == nil {
		t.Fatal("expected the source to be gone after purging its application")
	}
	if err := s.PurgeApplication(context.Background(), "never-had-one"); err != nil {
		t.Fatalf("purging an application with nothing kept is fine, got %v", err)
	}
}

func TestIDsCantEscapeTheRoot(t *testing.T) {
	s := New(t.TempDir())
	for _, bad := range [][2]string{{"../etc", "b"}, {"app", "../../x"}, {"a/b", "c"}, {"", "b"}} {
		if err := s.Save(bad[0], bad[1], []byte("x")); err == nil {
			t.Errorf("Save(%q, %q) should be refused", bad[0], bad[1])
		}
	}
	if err := s.PurgeApplication(context.Background(), ".."); err == nil {
		t.Error("PurgeApplication(\"..\") should be refused")
	}
}
