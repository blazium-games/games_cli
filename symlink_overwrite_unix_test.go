//go:build unix

package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

// symlinkRaceRounds is large enough that a lookup of the path followed by a
// separate open almost always observes the path change between the two calls.
const symlinkRaceRounds = 180

// flipBetweenFileAndSymlink atomically exchanges name in dir between a regular
// file and a symlink to secret until stop returns. The two directory entries
// exist for the whole race, so the writer cannot catch the name missing.
func flipBetweenFileAndSymlink(t *testing.T, dir, name, secret string) (stop func()) {
	t.Helper()
	regName := name + ".regular"
	symName := name + ".link"
	if err := os.WriteFile(filepath.Join(dir, regName), []byte("regular"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, symName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("regular"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stopped atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stopped.Load() {
			_ = unix.Renameat2(dirfd, symName, dirfd, name, unix.RENAME_EXCHANGE)
			_ = unix.Renameat2(dirfd, regName, dirfd, name, unix.RENAME_EXCHANGE)
		}
	}()
	return func() {
		stopped.Store(true)
		<-done
		_ = unix.Close(dirfd)
	}
}

// assertRaceKeptSecret fails when a writer followed the symlink, and also when
// the race never presented both a regular file (a successful write) and a
// symlink (a refusal). A check that looks the path up again before opening it
// still follows the link on some of these rounds.
func assertRaceKeptSecret(t *testing.T, secret string, rounds int, write func() error) {
	t.Helper()
	successes, refusals := 0, 0
	for i := 0; i < rounds; i++ {
		err := write()
		got, readErr := os.ReadFile(secret)
		if readErr != nil || string(got) != localFileSentinel {
			t.Fatalf("write followed a symlink swapped in between lookup and open (round %d): target = %q, %v", i, got, readErr)
		}
		if err != nil {
			refusals++
		} else {
			successes++
		}
	}
	if successes == 0 || refusals == 0 {
		t.Fatalf("race saw %d successful writes and %d refusals; want both, so the swap was live", successes, refusals)
	}
}

func TestLobbyDataExportSymlinkSwapBetweenCheckAndWrite(t *testing.T) {
	silenceCommandLogs(t)
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	stop := flipBetweenFileAndSymlink(t, dir, "export.json", secret)
	defer stop()
	cmd := lobbyExportCommand(t, lobbyExportServer(t))
	out := filepath.Join(dir, "export.json")
	assertRaceKeptSecret(t, secret, symlinkRaceRounds, func() error {
		return runLobbyDataExport(t, cmd, out)
	})
}

func TestExtractSapiPackSymlinkSwapBetweenCheckAndWrite(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	stop := flipBetweenFileAndSymlink(t, dir, "daily.luau", secret)
	defer stop()
	zipPath := writeTestZip(t, map[string]string{"daily.luau": "return 1\n"})
	assertRaceKeptSecret(t, secret, symlinkRaceRounds, func() error {
		_, err := extractSapiPack(zipPath, dir, true)
		return err
	})
}

func TestSapiScaffoldSymlinkSwapBetweenCheckAndWrite(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	stop := flipBetweenFileAndSymlink(t, dir, "daily.luau", secret)
	defer stop()
	assertRaceKeptSecret(t, secret, symlinkRaceRounds, func() error {
		_, _, err := sapiScaffold(dir, []string{"daily"}, true)
		return err
	})
}
