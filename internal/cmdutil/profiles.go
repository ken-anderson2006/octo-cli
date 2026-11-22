package cmdutil

import (
	"context"
	"os"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// ProfileSelector returns the explicitly requested profile, if any.
func (f *Factory) ProfileSelector() (string, error) {
	name := f.Globals.Profile
	if !f.Globals.ProfileSet && name == "" {
		name = os.Getenv(config.EnvProfile)
		if name == "" {
			return "", nil
		}
	}
	return name, authstore.ValidateName(name)
}

// RuntimeConfig returns the runtime target before applying a profile.
func (f *Factory) RuntimeConfig() *config.Config {
	cfg := config.LoadRequest()
	if f.Globals.Space != "" {
		cfg.SpaceID = f.Globals.Space
	}
	if f.Globals.Format != "" {
		cfg.Format = f.Globals.Format
	}
	return cfg
}

// ApplyProfileScope applies and checks the selected profile's origin and Space.
func ApplyProfileScope(cfg *config.Config, profile *authstore.Profile) (authstore.Profile, error) {
	p, err := profile.Scope()
	if err != nil {
		return authstore.Profile{}, err
	}
	if cfg.APIBaseURLSource == "env" {
		origin, err := config.NormalizeAPIBaseURL(cfg.APIBaseURL)
		if err != nil {
			return authstore.Profile{}, output.ErrValidation(err.Error(), "")
		}
		if origin != p.APIBaseURL {
			return authstore.Profile{}, scopeConflict("configured API origin does not match the selected profile")
		}
	}
	if cfg.SpaceID != "" && cfg.SpaceID != p.SpaceID {
		return authstore.Profile{}, scopeConflict("requested Space does not match the selected profile")
	}
	cfg.APIBaseURL, cfg.APIBaseURLSource, cfg.SpaceID = p.APIBaseURL, "profile", p.SpaceID
	return p, nil
}

func scopeConflict(message string) error {
	return output.ErrWithHint("validation", "PROFILE_SCOPE_MISMATCH", message, "select a matching profile or reimport a token for the new target")
}

type externalSource struct {
	tag    string
	value  string
	isFile bool
}

func (f *Factory) externalSource() (externalSource, error) {
	if f.requestBearer != "" {
		return externalSource{tag: "mcp:authorization", value: f.requestBearer}, nil
	}
	envToken, hasToken := os.LookupEnv(config.EnvToken)
	envFile, hasFile := os.LookupEnv(config.EnvTokenFile)
	switch {
	case hasToken && hasFile:
		return externalSource{}, output.ErrValidation("OCTO_TOKEN and OCTO_TOKEN_FILE cannot both be set", "unset one token environment variable")
	case f.Globals.CredentialFileSet || f.Globals.CredentialFile != "":
		return externalSource{tag: "flag:--credential-file", value: f.Globals.CredentialFile, isFile: true}, nil
	case hasToken:
		return externalSource{tag: "env:" + config.EnvToken, value: envToken}, nil
	case hasFile:
		return externalSource{tag: "env:" + config.EnvTokenFile, value: envFile, isFile: true}, nil
	default:
		if f.BotAPI {
			if token := strings.TrimSpace(os.Getenv(config.EnvBotToken)); token != "" {
				return externalSource{tag: "env:" + config.EnvBotToken, value: token}, nil
			}
		}
		return externalSource{}, nil
	}
}

func (s externalSource) read() (string, error) {
	token := s.value
	var err error
	if s.isFile {
		token, err = credential.ReadFile(s.value)
	} else {
		err = credential.ValidateBearer(token)
	}
	if err != nil {
		return "", output.ErrAuth(s.tag+": "+err.Error(), "check the selected credential source")
	}
	return token, nil
}

func (f *Factory) resolveRequest(ctx context.Context) (*config.Config, *credential.Credential, error) {
	if f.requestBearer == "" {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(config.EnvCredentialMode))) {
		case config.CredentialModeTask:
			return f.resolveTaskRequest()
		case "":
		default:
			return nil, nil, output.ErrValidation("unsupported OCTO_CREDENTIAL_MODE", "")
		}
	}
	external, err := f.externalSource()
	if err != nil {
		return nil, nil, err
	}
	name, err := f.ProfileSelector()
	if err != nil {
		return nil, nil, err
	}

	cfg := f.RuntimeConfig()
	resolved := &credential.Credential{}
	if name == "" && (!f.BotAPI || f.Globals.BotID == "") && external.tag != "" {
		resolved.Token, err = external.read()
		resolved.Source = external.tag
	} else {
		err = f.resolveProfile(ctx, cfg, name, external, resolved)
	}
	if err != nil {
		return nil, nil, err
	}

	cfg.APIBaseURL, err = config.NormalizeAPIBaseURL(cfg.APIBaseURL)
	if err != nil {
		return nil, nil, output.ErrValidation(err.Error(), "")
	}
	if cfg.SpaceID != "" {
		if err := authstore.ValidateSpace(cfg.SpaceID); err != nil {
			return nil, nil, err
		}
	}
	if resolved.Token == "" {
		return cfg, nil, nil
	}
	cfg.Token, resolved.SpaceID = resolved.Token, cfg.SpaceID
	if f.BotAPI && resolved.RobotID == "" {
		resolved.RobotID = strings.TrimSpace(os.Getenv(config.EnvBotID))
	}
	resolved.BotKind = credential.TokenKind(resolved.Token)
	return cfg, resolved, nil
}

func (f *Factory) resolveProfile(ctx context.Context, cfg *config.Config, selector string, external externalSource, resolved *credential.Credential) error {
	store, err := f.AuthStore()
	if err != nil {
		return err
	}
	return store.View(ctx, func(index *authstore.Config) error {
		name := selector
		if f.BotAPI && f.Globals.BotID != "" && name == "" {
			for candidate, profile := range index.Profiles {
				if profile.BotID != f.Globals.BotID {
					continue
				}
				if name != "" {
					return output.ErrValidation("multiple profiles match --bot-id", "select a profile by name")
				}
				name = candidate
			}
			if name == "" {
				return output.ErrAuth("Bot profile not found", "select an existing profile or import a Bot token")
			}
		}
		if name == "" {
			name = index.CurrentProfile
			if name == "" {
				return nil
			}
		}
		profile, ok := index.Profiles[name]
		if !ok {
			return output.ErrAuth("profile not found", "select an existing profile or import a token")
		}
		var p authstore.Profile
		if profile.BotID != "" {
			if !f.BotAPI {
				return output.ErrAuth("this profile is bound to the Bot API", "select a native Token profile")
			}
			if f.Globals.BotID != "" && f.Globals.BotID != profile.BotID {
				return output.ErrAuth("profile does not match the selected Bot", "")
			}
			p, err = profile.Scope()
			if err == nil {
				cfg.APIBaseURL, cfg.APIBaseURLSource = p.APIBaseURL, "profile"
				if f.Globals.Space == "" && p.SpaceID != "" {
					cfg.SpaceID = p.SpaceID
				}
				resolved.RobotID = p.BotID
				if !f.Globals.CredentialFileSet {
					external = externalSource{}
				}
			}
		} else {
			if f.BotAPI && f.Globals.BotID != "" {
				return output.ErrAuth("profile has no Bot ID binding", "")
			}
			p, err = ApplyProfileScope(cfg, &profile)
		}
		if err != nil {
			return err
		}
		if err := p.ValidateSource(); err != nil {
			return err
		}

		resolved.Profile = name
		if external.tag != "" {
			resolved.Source = external.tag
			resolved.Token, err = external.read()
			return err
		}
		resolved.Source = p.TokenSource.Type
		resolved.Token, err = store.ReadToken(ctx, &p)
		return err
	})
}

func (f *Factory) resolveTaskRequest() (*config.Config, *credential.Credential, error) {
	if f.Globals.ProfileSet || f.Globals.Profile != "" || f.Globals.BotID != "" ||
		os.Getenv(config.EnvProfile) != "" || os.Getenv(config.EnvBotID) != "" ||
		f.Globals.CredentialFileSet || f.Globals.CredentialFile != "" || f.Globals.Space != "" {
		return nil, nil, output.ErrValidation("task mode does not allow profile, bot, Space, or credential selectors", "supply the Loop workspace argument")
	}
	cfg := f.RuntimeConfig()
	cfg.SpaceID = ""
	cfg.Token = strings.TrimSpace(os.Getenv(config.EnvTaskToken))
	if cfg.Token == "" {
		return nil, nil, output.ErrAuth("task mode requires OCTO_TASK_TOKEN", "")
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, output.ErrAuth("invalid task credential or API origin", "")
	}
	return cfg, &credential.Credential{Token: cfg.Token, Source: "env:" + config.EnvTaskToken, BotKind: "agent_task"}, nil
}
