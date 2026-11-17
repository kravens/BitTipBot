package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/LightningTipBot/LightningTipBot/internal"
	"github.com/LightningTipBot/LightningTipBot/internal/netguard"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
)

type ClientType string

const (
	ClientTypeClearNet = "clearnet"
	ClientTypeTor      = "tor"
)

// checks if strings contains http(s)://*.onion
var isOnion = regexp.MustCompile("^https?\\:\\/\\/[\\w\\-\\.]+\\.onion")

// GetClientForScheme returns correct client for url scheme.
// if tld is .onion, function will also return an onion client.
func GetClientForScheme(url *url.URL) (*http.Client, error) {
	if isOnion.FindString(url.String()) != "" {
		return GetClient(ClientTypeTor)
	}
	switch url.Scheme {
	case "onion":
		return GetClient(ClientTypeTor)
	default:
		return GetClient(ClientTypeClearNet)
	}
}

// directTransport returns the transport used when no proxy is configured. The
// URLs fetched with these clients come from users and remote servers, so it
// only connects to public addresses. Every client gets its own transport,
// because callers change its TLS settings (satdress.SetupHttpClient).
func directTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = netguard.DialContext
	return t
}

// The bot's own hosts may resolve to loopback (hosts file, split DNS). Paying
// a lightning address on them must keep working.
func init() {
	if u := internal.Configuration.Bot.LNURLHostUrl; u != nil {
		netguard.Trust(hostPort(u))
	}
	if u, err := url.Parse(internal.Configuration.Lnbits.Url); err == nil {
		netguard.Trust(hostPort(u))
	}
}

// hostPort returns host:port of u, with the scheme's default port.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return ""
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func GetClient(clientType ClientType) (*http.Client, error) {
	client := http.Client{
		Timeout:   10 * time.Second,
		Transport: directTransport(),
	}
	var cfg *internal.SocksConfiguration
	switch clientType {
	case ClientTypeClearNet:
		cfg = internal.Configuration.Bot.SocksProxy
	case ClientTypeTor:
		cfg = internal.Configuration.Bot.TorProxy
	default:
		return nil, fmt.Errorf("[GetClient] invalid clientType")
	}
	if cfg == nil {
		return &client, nil
	}
	if cfg.Host == "" {
		return &client, nil
	}
	proxyURL, _ := url.Parse(cfg.Host)
	specialTransport := &http.Transport{}
	specialTransport.Proxy = http.ProxyURL(proxyURL)
	var auth *proxy.Auth
	if cfg.Username != "" && cfg.Password != "" {
		auth = &proxy.Auth{User: cfg.Username, Password: cfg.Password}
	}
	d, err := proxy.SOCKS5("tcp", cfg.Host, auth, &net.Dialer{
		Timeout:   20 * time.Second,
		Deadline:  time.Now().Add(time.Second * 10),
		KeepAlive: -1,
	})
	if err != nil {
		log.Errorln(err)
		return &client, nil
	}
	specialTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// The proxy resolves hostnames, so only literal addresses and
		// localhost names can be refused here.
		if err := netguard.CheckHost(addr); err != nil {
			return nil, err
		}
		return d.Dial(network, addr)
	}
	client.Transport = specialTransport
	return &client, nil
}

// MaxResponseBody is the most ReadBody reads. LNURL and node API answers are a
// few KB; servers chosen by users must not make the bot buffer more, and
// transparent gzip turns a few hundred KB on the wire into gigabytes.
const MaxResponseBody = 1 << 20

// ReadBody reads a response body of at most MaxResponseBody bytes.
func ReadBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxResponseBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxResponseBody {
		return nil, fmt.Errorf("response larger than %d bytes", MaxResponseBody)
	}
	return b, nil
}
