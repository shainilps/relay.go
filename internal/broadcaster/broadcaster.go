package broadcaster

import (
	"net/http"
	"time"

	"github.com/shainilps/relay/internal/config"
	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Broadcaster struct {
	Arc      *ArcPool
	Explorer *WOCExplorer
}

func NewBroadcaster() *Broadcaster {
	network := config.Network()

	providers := []*Arc{NewTaalArcProvider(network, viper.GetString("arc.token"))}

	fallbackURL := viper.GetString("arc.fallback_url")
	if fallbackURL == "" && network == model.MAIN {
		fallbackURL = GorillaPoolMainURL
	}
	if fallbackURL != "" {
		providers = append(providers, NewArc("fallback", fallbackURL, viper.GetString("arc.fallback_token")))
	}

	return &Broadcaster{
		Arc:      NewArcPool(providers...),
		Explorer: NewWOCExplorerProvider(network, viper.GetString("woc.token")),
	}
}

func httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
}
