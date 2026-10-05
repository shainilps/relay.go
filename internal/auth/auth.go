package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/shainilps/relay/internal/telemetry"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Mode string

const (
	ModeNone  Mode = "none"
	ModeToken Mode = "token"
	ModeBasic Mode = "basic"

	MIN_TOKEN_LENGTH    = 32
	MIN_PASSWORD_LENGTH = 12
)

type Token struct {
	Name  string `mapstructure:"name"`
	Token string `mapstructure:"token"`
}

type User struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

type Config struct {
	Mode   Mode    `mapstructure:"mode"`
	Tokens []Token `mapstructure:"tokens"`
	Users  []User  `mapstructure:"users"`
}

type credential struct {
	client string
	secret [sha256.Size]byte
	user   [sha256.Size]byte
}

type Authenticator struct {
	mode        Mode
	credentials []credential
}

type clientKey struct{}

func ClientFrom(ctx context.Context) string {
	client, _ := ctx.Value(clientKey{}).(string)
	return client
}

func Load() (*Authenticator, error) {
	var config Config
	if err := viper.UnmarshalKey("auth", &config); err != nil {
		return nil, err
	}
	return New(config)
}

func New(config Config) (*Authenticator, error) {
	authenticator := &Authenticator{mode: config.Mode}
	seen := map[string]bool{}

	switch config.Mode {
	case ModeNone:
		zap.L().Warn("auth.mode is none, every endpoint is open to anyone who can reach it")

	case ModeToken:
		if len(config.Tokens) == 0 {
			return nil, errors.New("auth.mode is token but auth.tokens is empty")
		}
		for _, token := range config.Tokens {
			if token.Name == "" {
				return nil, errors.New("every auth token needs a name")
			}
			if seen[token.Name] {
				return nil, fmt.Errorf("auth token name %q is used twice", token.Name)
			}
			seen[token.Name] = true
			if len(token.Token) < MIN_TOKEN_LENGTH {
				return nil, fmt.Errorf("auth token %q must be at least %d characters", token.Name, MIN_TOKEN_LENGTH)
			}
			authenticator.credentials = append(authenticator.credentials, credential{
				client: token.Name,
				secret: sha256.Sum256([]byte(token.Token)),
			})
		}

	case ModeBasic:
		if len(config.Users) == 0 {
			return nil, errors.New("auth.mode is basic but auth.users is empty")
		}
		for _, user := range config.Users {
			if user.Username == "" {
				return nil, errors.New("every auth user needs a username")
			}
			if seen[user.Username] {
				return nil, fmt.Errorf("auth username %q is used twice", user.Username)
			}
			seen[user.Username] = true
			if len(user.Password) < MIN_PASSWORD_LENGTH {
				return nil, fmt.Errorf("auth password for %q must be at least %d characters", user.Username, MIN_PASSWORD_LENGTH)
			}
			authenticator.credentials = append(authenticator.credentials, credential{
				client: user.Username,
				user:   sha256.Sum256([]byte(user.Username)),
				secret: sha256.Sum256([]byte(user.Password)),
			})
		}

	case "":
		return nil, errors.New("auth.mode must be set to none, token or basic")

	default:
		return nil, fmt.Errorf("auth.mode must be none, token or basic, got %q", config.Mode)
	}

	return authenticator, nil
}

func (a *Authenticator) matchToken(presented string) (string, bool) {
	digest := sha256.Sum256([]byte(presented))
	client := ""
	for _, credential := range a.credentials {
		if subtle.ConstantTimeCompare(digest[:], credential.secret[:]) == 1 {
			client = credential.client
		}
	}
	return client, client != ""
}

func (a *Authenticator) matchUser(username string, password string) (string, bool) {
	userDigest := sha256.Sum256([]byte(username))
	passwordDigest := sha256.Sum256([]byte(password))
	client := ""
	for _, credential := range a.credentials {
		userMatch := subtle.ConstantTimeCompare(userDigest[:], credential.user[:])
		passwordMatch := subtle.ConstantTimeCompare(passwordDigest[:], credential.secret[:])
		if userMatch&passwordMatch == 1 {
			client = credential.client
		}
	}
	return client, client != ""
}

func (a *Authenticator) authenticate(r *http.Request) (string, bool) {
	switch a.mode {
	case ModeNone:
		return "anonymous", true

	case ModeToken:
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return "", false
		}
		return a.matchToken(token)

	case ModeBasic:
		username, password, ok := r.BasicAuth()
		if !ok {
			return "", false
		}
		return a.matchUser(username, password)
	}

	return "", false
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, ok := a.authenticate(r)
		if !ok {
			telemetry.Count(r.Context(), telemetry.Metrics.AuthRejected, attribute.String("path", r.URL.Path))
			telemetry.Log(r.Context()).Warn("rejected unauthenticated request", zap.String("remote_addr", r.RemoteAddr), zap.String("path", r.URL.Path))
			if a.mode == ModeBasic {
				w.Header().Set("WWW-Authenticate", `Basic realm="relay", charset="UTF-8"`)
			} else {
				w.Header().Set("WWW-Authenticate", `Bearer realm="relay"`)
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("relay.client", client))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, client)))
	})
}
