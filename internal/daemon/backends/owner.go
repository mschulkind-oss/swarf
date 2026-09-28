package backends

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

// OwnerTokenFile is the name of the owner token inside a store's .git/.
//
// An owner token is a random value generated once per local store. Because
// it lives in .git/, every rclone sync carries it to
// <remote>/machines/<machine_id>/, so before uploading a machine can tell
// whether the subtree it is about to overwrite was written by this store
// or by a different store configured with the same machine_id (a shared
// hostname, or a config.toml copied with dotfiles). Git never reads or
// cleans the file.
const OwnerTokenFile = "swarf-owner"

// OwnerTokenPath returns where storePath keeps its owner token.
func OwnerTokenPath(storePath string) string {
	return filepath.Join(storePath, ".git", OwnerTokenFile)
}

// ReadOwnerToken returns storePath's owner token, if it has one yet.
func ReadOwnerToken(storePath string) (string, bool) {
	data, err := os.ReadFile(OwnerTokenPath(storePath))
	if err != nil {
		return "", false
	}
	tok := strings.TrimSpace(string(data))
	return tok, tok != ""
}

// EnsureOwnerToken returns storePath's owner token, generating and saving
// one on first use.
func EnsureOwnerToken(storePath string) (string, error) {
	if tok, ok := ReadOwnerToken(storePath); ok {
		return tok, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate owner token: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(OwnerTokenPath(storePath), []byte(tok+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write owner token: %w", err)
	}
	return tok, nil
}

// RemoteOwnerToken reads the owner token in the machine subtree target
// (see MachineRemote). ok is false when the subtree, or its token, does
// not exist: nothing has claimed it yet. err is set only when the remote
// could not be read, so the caller cannot tell either way.
func RemoteOwnerToken(ctx context.Context, target string) (tok string, ok bool, err error) {
	path := strings.TrimRight(target, "/") + "/.git/" + OwnerTokenFile
	out, err := exec.CommandContext(ctx, "rclone", "cat", path).Output()
	if err != nil {
		// rclone exits 3 (directory not found) or 4 (file not found)
		// when the path is missing; anything else is a real failure.
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 3 || exit.ExitCode() == 4) {
			return "", false, nil
		}
		msg := err.Error()
		if exit != nil && len(exit.Stderr) > 0 {
			msg = strings.TrimSpace(string(exit.Stderr))
		}
		return "", false, fmt.Errorf("rclone cat %s: %s", path, msg)
	}
	// Remotes without real directories (object stores) answer a missing
	// path with an empty listing rather than an error.
	tok = strings.TrimSpace(string(out))
	return tok, tok != "", nil
}

// OwnerMismatch is the explanation, with the ways out, for a machine
// subtree whose owner token belongs to a different store.
//
// The take-over advice is explicit about what is lost: 'swarf push
// --take-over' replaces the subtree with a destructive rclone sync, and
// 'swarf pull' skips this machine's own id, so pulling first brings in
// the other machines' changes but never the subtree being replaced.
func OwnerMismatch(machineID, target string) string {
	return fmt.Sprintf("%s was written by a different store that also uses machine_id %q.\n"+
		"  If this is a different computer: give it its own id under [machine] id in %s.\n"+
		"  If this computer replaces that one: 'swarf push --take-over' replaces that subtree with this store and deletes from the remote anything only the old store had there; 'swarf pull' first merges the other machines' subtrees, not this one, so copy out anything you need beforehand (rclone copy %s <dir>).",
		target, machineID, paths.GlobalConfigTOML, target)
}
