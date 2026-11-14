package price

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type PriceWatcher struct {
	client         *http.Client
	UpdateInterval time.Duration
	Currencies     map[string]string
	Exchanges      map[string]func(string) (float64, error)
}

var (
	// prices is written by Watch and read by request handlers; use Get.
	prices   = map[string]float64{}
	pricesMu sync.RWMutex
	P        *PriceWatcher
)

// Get returns the last average price of one bitcoin in currency, or 0 when
// none is known yet.
func Get(currency string) float64 {
	pricesMu.RLock()
	defer pricesMu.RUnlock()
	return prices[currency]
}

func NewPriceWatcher() *PriceWatcher {
	pricewatcher := &PriceWatcher{
		client: &http.Client{
			Timeout: time.Second * time.Duration(5),
		},
		// attention: $ must come after other $-denominated currencies like R$
		Currencies: map[string]string{
			"EUR": "€",
			"GBP": "£",
			"JPY": "¥",
			"BRL": "R$",
			"MXN": "MX$",
			"USD": "$",
			"RUB": "₽",
			"TRY": "₺",
			"INR": "₹",
		},
		Exchanges:      make(map[string]func(string) (float64, error), 0),
		UpdateInterval: time.Second * time.Duration(30),
	}
	pricewatcher.Exchanges["coinbase"] = pricewatcher.GetCoinbasePrice
	pricewatcher.Exchanges["bitfinex"] = pricewatcher.GetBitfinexPrice
	log.Infof("[PriceWatcher] Watcher started")
	P = pricewatcher
	return pricewatcher
}

func (p *PriceWatcher) Start() {
	go p.Watch()
}

func (p *PriceWatcher) Watch() error {
	for {
		for currency, _ := range p.Currencies {
			avg_price := 0.0
			n_responses := 0
			for _, getPrice := range p.Exchanges {
				fprice, err := getPrice(currency)
				if err != nil {
					// log.Debug(err)
					// if one exchanges is down, use the next
					continue
				}
				n_responses++
				avg_price += fprice
				// log.Debugf("[PriceWatcher] %s %s price: %f", exchange, currency, fprice)
				time.Sleep(time.Second * time.Duration(2))
			}
			if n_responses == 0 {
				// keep the last known price instead of storing NaN
				continue
			}
			pricesMu.Lock()
			prices[currency] = avg_price / float64(n_responses)
			pricesMu.Unlock()
		}
		time.Sleep(p.UpdateInterval)
	}
}

func (p *PriceWatcher) GetCoinbasePrice(currency string) (float64, error) {
	coinbaseEndpoint, err := url.Parse(fmt.Sprintf("https://api.coinbase.com/v2/prices/spot?currency=%s", currency))
	response, err := p.client.Get(coinbaseEndpoint.String())
	if err != nil {
		return 0, err
	}
	bodyBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Debug(err)
		return 0, err
	}
	price := gjson.Get(string(bodyBytes), "data.amount")
	if len(price.String()) > 0 {
		fprice, err := strconv.ParseFloat(strings.TrimSpace(price.String()), 64)
		if err != nil {
			log.Debug(err)
			return 0, err
		}
		return fprice, nil
	} else {
		return 0, fmt.Errorf("no price")
	}
}

func (p *PriceWatcher) GetBitfinexPrice(currency string) (float64, error) {
	var bitfinexCurrencyToPair = map[string]string{"USD": "btcusd", "EUR": "btceur", "GBP": "btcusd", "JPY": "btcjpy"}
	pair := bitfinexCurrencyToPair[currency]
	bitfinexEndpoint, err := url.Parse(fmt.Sprintf("https://api.bitfinex.com/v1/pubticker/%s", pair))
	response, err := p.client.Get(bitfinexEndpoint.String())
	if err != nil {
		return 0, err
	}
	bodyBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Debug(err)
		return 0, err
	}
	price := gjson.Get(string(bodyBytes), "last_price")
	if len(price.String()) > 0 {
		fprice, err := strconv.ParseFloat(strings.TrimSpace(price.String()), 64)
		if err != nil {
			log.Debug(err)
			return 0, err
		}
		return fprice, nil
	} else {
		return 0, fmt.Errorf("no price")
	}
}
