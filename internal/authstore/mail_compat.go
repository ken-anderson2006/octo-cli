package authstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// errMailCredentialNotFound indicates that the selected Bot has not completed
// Agent Mail authorization.
var errMailCredentialNotFound = errors.New("mail credential not found")

// errMailCredentialAmbiguous means the current Bot token has Mail bindings in
// more than one Space and the caller did not select one.
var errMailCredentialAmbiguous = errors.New("multiple mail credentials match the active Bot")

// mailBinding identifies the local namespace selected for a mailbox token.
type mailBinding struct {
	Key     string
	RobotID string
	SpaceID string
}

func parseMailBindingKey(key string) (binding mailBinding, originFingerprint, tokenFingerprint string, ok bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 5 || parts[0] != "v2" || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return mailBinding{}, "", "", false
	}
	robotID, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(robotID) == 0 {
		return mailBinding{}, "", "", false
	}
	spaceID, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(spaceID) == 0 {
		return mailBinding{}, "", "", false
	}
	return mailBinding{Key: key, RobotID: string(robotID), SpaceID: string(spaceID)}, parts[3], parts[4], true
}

func matchingMailBindings(keys []string, robotID, spaceID, botToken, apiOrigin string) ([]mailBinding, error) {
	if botToken == "" {
		return nil, errors.New("Bot token is required") //nolint:staticcheck // Bot is the product's canonical identity term.
	}
	normalizedOrigin, err := config.NormalizeAPIBaseURL(apiOrigin)
	if err != nil {
		return nil, err
	}
	originFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(normalizedOrigin)))
	tokenFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(botToken)))
	matches := make([]mailBinding, 0, 1)
	for _, key := range keys {
		binding, storedOriginFingerprint, storedTokenFingerprint, ok := parseMailBindingKey(key)
		if !ok || storedOriginFingerprint != originFingerprint || storedTokenFingerprint != tokenFingerprint {
			continue
		}
		if robotID != "" && binding.RobotID != robotID {
			continue
		}
		if spaceID != "" && binding.SpaceID != spaceID {
			continue
		}
		matches = append(matches, binding)
	}
	return matches, nil
}

// ReadBotMailToken reads the existing encrypted Bot-bound Mail credential.
// A match requires the same Bot token and normalized origin.
func (s *Store) ReadBotMailToken(bot *credential.Credential, origin string) (string, error) {
	if bot == nil {
		return "", output.ErrAuth("Bot credential required for Agent Mail", "")
	}
	dir := s.dir
	if os.Getenv(EnvConfigDir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", output.ErrAuth("Agent Mail credential directory is unavailable", "")
		}
		dir = filepath.Join(home, ".octo-cli")
	}
	legacy := Store{dir: dir}
	exists, err := legacy.checkDir(false)
	if err != nil {
		return "", output.ErrAuth("Agent Mail credential directory is unavailable", "")
	}
	if !exists {
		return "", output.ErrAuth(errMailCredentialNotFound.Error(), "")
	}
	blob, err := readPrivate(filepath.Join(dir, "mail-credentials.enc"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", output.ErrAuth(errMailCredentialNotFound.Error(), "")
	}
	if err != nil {
		return "", output.ErrAuth("could not read encrypted Agent Mail credentials", "")
	}
	salt, err := readPrivate(filepath.Join(dir, "cred.salt"))
	if err != nil || len(salt) != sha256.Size {
		return "", output.ErrAuth("Agent Mail credential key is unavailable", "")
	}
	id, err := machineID()
	if err != nil && !errors.Is(err, errMachineIDUnsupported) {
		return "", output.ErrAuth("Agent Mail credential key is unavailable", "")
	}
	key := sha256.Sum256(append([]byte(id), salt...))
	aead, err := newGCM(key[:])
	if err != nil {
		return "", output.ErrAuth("Agent Mail credentials could not be opened", "")
	}
	plain, err := aead.Open(nil, nil, blob, nil)
	var tokens map[string]string
	if err != nil || json.Unmarshal(plain, &tokens) != nil {
		return "", output.ErrAuth("Agent Mail credentials could not be opened", "")
	}
	keys := make([]string, 0, len(tokens))
	for key := range tokens {
		keys = append(keys, key)
	}
	bindings, err := matchingMailBindings(keys, bot.RobotID, bot.SpaceID, bot.Token, origin)
	if err != nil || len(bindings) == 0 {
		return "", output.ErrAuth(errMailCredentialNotFound.Error(), "")
	}
	if len(bindings) > 1 {
		return "", output.ErrValidation(errMailCredentialAmbiguous.Error(), "select the matching Space")
	}
	token := tokens[bindings[0].Key]
	if err := credential.ValidateBearer(token); err != nil {
		return "", output.ErrAuth("invalid stored Agent Mail credential", "")
	}
	return token, nil
}
