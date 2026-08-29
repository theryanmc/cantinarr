// Package mediaserver defines the provider-neutral contract Cantinarr uses to
// manage user accounts on a media server (Jellyfin and Emby). It is a
// leaf package: value types, sentinel errors, and the Provider interface the
// per-server clients implement. Nothing here dials anything.
package mediaserver

import (
	"context"
	"errors"
	"unicode"
)

// SystemInfo identifies a media server; the connection test reads it.
type SystemInfo struct {
	ServerName string
	Version    string
	ID         string
}

// Library is one library the media server reports. ID is the identifier the
// server's user policy expects when restricting an account to specific
// libraries — never a filesystem path.
type Library struct {
	ID             string
	Name           string
	CollectionType string
}

// RemoteUser is the subset of a media-server account Cantinarr acts on.
type RemoteUser struct {
	ID              string
	Name            string
	IsAdministrator bool
	IsDisabled      bool
}

var (
	// ErrUserExists reports that an account with the requested name already
	// exists on the media server (names compare case-insensitively).
	ErrUserExists = errors.New("media server user already exists")
	// ErrUserNotFound reports that no account has the given remote id.
	ErrUserNotFound = errors.New("media server user not found")
	// ErrInvalidName reports a name the media server would refuse.
	ErrInvalidName = errors.New("name is not valid on the media server")
)

// Provider is what a media-server client must offer. Implementations keep
// hosts and credentials out of every error they return: these errors can
// reach requesters.
type Provider interface {
	SystemInfo(ctx context.Context) (SystemInfo, error)
	Libraries(ctx context.Context) ([]Library, error)
	Users(ctx context.Context) ([]RemoteUser, error)
	GetUser(ctx context.Context, remoteID string) (RemoteUser, error)
	// CreateUser validates the name (ErrInvalidName), refuses a name that is
	// already taken (ErrUserExists), creates the account with the password,
	// and restricts it to libraryIDs (empty = every library) as a
	// non-administrator. When any step after creation fails the half-made
	// account is deleted, so no unrestricted or passwordless account is left
	// behind.
	CreateUser(ctx context.Context, name, password string, libraryIDs []string) (RemoteUser, error)
	SetLibraries(ctx context.Context, remoteID string, libraryIDs []string) error
	SetDisabled(ctx context.Context, remoteID string, disabled bool) error
	// DeleteUser exists for rollback only: Cantinarr never deletes an account
	// it did not just create.
	DeleteUser(ctx context.Context, remoteID string) error
}

// ValidUsername mirrors Jellyfin's rule for account names
// (^(?!\s)[\w\ \-'._@+]+(?<!\s)$ with .NET's Unicode \w): at least one
// character, no leading or trailing whitespace, and every rune a letter,
// digit, combining mark, connector punctuation, or one of space - ' . _ @ +.
// Cantinarr usernames are validated only for emptiness, so this is checked
// before any account is created rather than discovered as a remote 400. Emby's
// own rule is closed-source; the last open-source one refused only < and >,
// so this mirror is the stricter of the two and never admits a name Emby
// would refuse on that rule.
func ValidUsername(name string) bool {
	if name == "" {
		return false
	}
	runes := []rune(name)
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return false
	}
	for _, r := range runes {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.Is(unicode.Mn, r), unicode.Is(unicode.Pc, r):
		case r == ' ', r == '-', r == '\'', r == '.', r == '_', r == '@', r == '+':
		default:
			return false
		}
	}
	return true
}
