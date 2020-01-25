/*
 * Copyright (C) 2020 The "MysteriumNetwork/node" Authors.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package service

import (
	"encoding/json"
	"fmt"

	"github.com/mysteriumnetwork/node/core/location"
	"github.com/mysteriumnetwork/node/core/port"
	"github.com/mysteriumnetwork/node/identity"
	"github.com/mysteriumnetwork/node/market"
	"github.com/mysteriumnetwork/node/nat/event"
	"github.com/mysteriumnetwork/node/nat/mapping"
	"github.com/mysteriumnetwork/node/nat/traversal"
	"github.com/mysteriumnetwork/node/services/openvpn/discovery/dto"
	"github.com/mysteriumnetwork/node/services/socks5"
	sock5_session "github.com/mysteriumnetwork/node/services/socks5/session"
	"github.com/mysteriumnetwork/node/session"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// GetProposal returns the proposal for SOCKS5 service
func GetProposal(location location.Location) market.ServiceProposal {
	marketLocation := market.Location{
		Continent: location.Continent,
		Country:   location.Country,
		City:      location.City,

		ASN:      location.ASN,
		ISP:      location.ISP,
		NodeType: location.NodeType,
	}

	return market.ServiceProposal{
		ServiceType: socks5.ServiceType,
		ServiceDefinition: socks5.ServiceDefinition{
			Location:          marketLocation,
			LocationOriginate: marketLocation,
		},
		PaymentMethodType: dto.PaymentMethodPerTime,
		PaymentMethod:     dto.DefaultPaymentInfo,
	}
}

// NATPinger defined Pinger interface for Provider
type NATPinger interface {
	BindServicePort(key string, port int)
	Stop()
	Valid() bool
}

// NATEventGetter allows us to fetch the last known NAT event
type NATEventGetter interface {
	LastEvent() *event.Event
}

// NewManager creates new instance of SOCKS5 service
func NewManager(
	options Options,
	location location.ServiceLocationInfo,
	natPort func(port int) (natRelease func()),
	natEventGetter NATEventGetter,
	natPinger NATPinger,
) *Manager {
	return &Manager{
		natPort:        natPort,
		natPinger:      natPinger,
		natEventGetter: natEventGetter,
		natPingerPorts: port.NewPool(),

		servicePort: options.Port.Num(),
		publicIP:    location.PubIP,
		outboundIP:  location.OutIP,

		done: make(chan struct{}),
	}
}

// Manager represents an instance of SOCKS5 service
type Manager struct {
	natPort        func(int) (natPortRelease func())
	natPinger      NATPinger
	natPingerPorts port.ServicePortSupplier
	natEventGetter NATEventGetter

	servicePort int
	publicIP    string
	outboundIP  string

	done chan struct{}
}

// ProvideConfig provides the config for consumer and handles new SOCKS5 connection.
func (m *Manager) ProvideConfig(sessionRequestData json.RawMessage) (*session.ConfigParams, error) {
	if m.servicePort == 0 {
		return nil, errors.New("Service port not initialized")
	}

	sessionConfig := sock5_session.Response{
		Port: m.servicePort,
	}
	sessionDestroy := func() {
		// Do nothing
	}

	sessionRequest := sock5_session.Request{}
	err := json.Unmarshal(sessionRequestData, &sessionRequest)
	if err != nil {
		return nil, errors.Wrap(err, "could not unmarshal SOCKS5 session request")
	}

	natPingerEnabled := m.natPinger.Valid() && m.isBehindNAT() && m.portMappingFailed()
	traversalParams, err := m.newTraversalParams(natPingerEnabled, sessionRequest)
	if err != nil {
		return nil, errors.Wrap(err, "could not create traversal params")
	}

	if natPingerEnabled {
		log.Info().Msgf("NAT Pinger enabled, binding service port for proxy, key: %s", traversalParams.ProxyPortMappingKey)
		m.natPinger.BindServicePort(traversalParams.ProxyPortMappingKey, sessionConfig.Port)
		sessionConfig.PortNat = traversalParams.ConsumerPort
		sessionConfig.Port = traversalParams.ProviderPort
	}

	return &session.ConfigParams{SessionServiceConfig: sessionConfig, SessionDestroyCallback: sessionDestroy, TraversalParams: &traversalParams}, nil
}

// Serve starts service - does block
func (m *Manager) Serve(providerID identity.Identity) error {
	releasePorts := m.natPort(m.servicePort)
	defer releasePorts()

	log.Info().Msg("SOCKS5 service started successfully now")
	<-m.done
	return nil
}

// Stop stops service.
func (m *Manager) Stop() error {
	close(m.done)

	log.Info().Msg("SOCKS5 service stopped")
	return nil
}

func (m *Manager) isBehindNAT() bool {
	return m.outboundIP != m.publicIP
}

func (m *Manager) portMappingFailed() bool {
	lastEvent := m.natEventGetter.LastEvent()
	if lastEvent == nil {
		return false
	}

	if lastEvent.Stage == traversal.StageName {
		return true
	}
	return lastEvent.Stage == mapping.StageName && !lastEvent.Successful
}

func (m *Manager) newTraversalParams(natPingerEnabled bool, sessionRequest sock5_session.Request) (traversal.Params, error) {
	params := traversal.Params{
		Cancel: make(chan struct{}),
	}
	if !natPingerEnabled {
		return params, nil
	}

	cp, err := m.natPingerPorts.Acquire()
	if err != nil {
		return params, errors.Wrap(err, "could not acquire NAT pinger consumer port")
	}

	params.ProviderPort = m.servicePort
	params.ConsumerPort = cp.Num()
	params.ProxyPortMappingKey = fmt.Sprintf("%s_%d", socks5.ServiceType, params.ProviderPort)

	if sessionRequest.IP == "" {
		return params, errors.New("remote party does not support NAT Hole punching, public IP is missing")
	}
	params.ConsumerPublicIP = sessionRequest.IP

	return params, nil
}
