package interbroker

import (
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jorgebay/soda/internal/conf"
	"github.com/jorgebay/soda/internal/types"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/http2"
)

const (
	baseReconnectionDelay = 20
	maxReconnectionDelay  = 60_000
)

type clientMap map[int]*clientInfo

func (g *gossiper) OpenConnections() error {
	// Open connections in the background
	c := make(chan bool, 1)
	go func() {
		c <- true
		// We could use a single client for all peers but to
		// reduce contention and having more fine grained control, we use one per each peer
		g.connectionsMutex.Lock()
		defer g.connectionsMutex.Unlock()
		peers := g.discoverer.Peers()
		m := make(clientMap, len(peers))
		var wg sync.WaitGroup
		for _, peer := range peers {
			wg.Add(1)
			clientInfo := g.createClient(&peer)
			log.Debug().Msgf("Before first connection, is up? %v", clientInfo.isHostUp())
			m[peer.Ordinal] = clientInfo
			go func(p *types.BrokerInfo) {
				_, err := clientInfo.client.Get(g.GetPeerUrl(p, conf.StatusUrl))

				wg.Done()
				if err != nil {
					// Reconnection will continue in the background as part of transport logic
					log.Err(err).Msgf("Initial connection to peer %s failed", p)
				} else {
					log.Debug().Msgf("Connected to peer %s", p)
				}
			}(&peer)
		}
		wg.Wait()

		g.connections.Store(m)
	}()

	<-c
	log.Info().Msg("Start opening connections to peers")

	return nil
}

func (g *gossiper) createClient(broker *types.BrokerInfo) *clientInfo {
	var connection atomic.Value

	clientInfo := &clientInfo{connection: &connection, hostName: broker.HostName}

	clientInfo.client = &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			// Pretend we are dialing a TLS endpoint
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				// When in-flight streams is below max, there's a single open connection
				log.Debug().Msgf("Creating connection to %s", addr)
				conn, err := net.Dial(network, addr)
				if err != nil {
					// Clean whatever is in cache with a connection marked as closed
					connection.Store(newFailedConnection())
					clientInfo.startReconnection(g, broker)
					return conn, err
				}

				c := newOpenConnection(conn, func() { clientInfo.startReconnection(g, broker) })

				// Store it at clientInfo level to retrieve the connection status later
				connection.Store(c)
				return c, nil
			},
			// Use an eager health check setting at the cost of a few bytes/sec
			ReadIdleTimeout: 200 * time.Millisecond,
			PingTimeout:     400 * time.Millisecond,
		},
	}

	return clientInfo
}

func (g *gossiper) GetPeerUrl(b *types.BrokerInfo, path string) string {
	return fmt.Sprintf("http://%s:%d%s", b.HostName, g.config.GossipPort(), path)
}

func (g *gossiper) getClientInfo(broker *types.BrokerInfo) *clientInfo {
	if m, ok := g.connections.Load().(clientMap); ok {
		if clientInfo, ok := m[broker.Ordinal]; ok {
			return clientInfo
		}
	}

	return nil
}

type clientInfo struct {
	client         *http.Client
	connection     *atomic.Value
	hostName       string
	isReconnecting int32
}

// isHostUp determines whether a host is considered UP
func (cli *clientInfo) isHostUp() bool {
	c, ok := cli.connection.Load().(*connectionWrapper)
	return ok && c.isOpen
}

// startReconnection starts reconnection in the background if it hasn't started
func (c *clientInfo) startReconnection(g *gossiper, broker *types.BrokerInfo) {
	// Determine is already reconnecting
	if !atomic.CompareAndSwapInt32(&c.isReconnecting, 0, 1) {
		return
	}

	go func() {
		i := 0
		for {
			delay := math.Pow(2, float64(i)) * baseReconnectionDelay
			if delay > maxReconnectionDelay {
				delay = maxReconnectionDelay
			} else {
				i++
			}
			// TODO: Add jitter
			time.Sleep(time.Duration(delay) * time.Millisecond)

			if c := g.getClientInfo(broker); c == nil || c.hostName != broker.HostName {
				// Topology changed, stop reconnecting
				break
			}

			log.Debug().Msgf("Attempting to reconnect to %s after %v ms", broker, delay)

			_, err := c.client.Get(g.GetPeerUrl(broker, conf.StatusUrl))
			if err == nil {
				// Succeeded
				break
			}
		}

		// Leave field as 0 to allow new reconnections
		atomic.StoreInt32(&c.isReconnecting, 0)
	}()
}