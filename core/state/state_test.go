/*
 * Copyright (C) 2019 The "MysteriumNetwork/node" Authors.
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

package state

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mysteriumnetwork/node/consumer/session"
	"github.com/mysteriumnetwork/node/core/connection"
	"github.com/mysteriumnetwork/node/core/service"
	"github.com/mysteriumnetwork/node/core/service/servicestate"
	"github.com/mysteriumnetwork/node/datasize"
	"github.com/mysteriumnetwork/node/eventbus"
	"github.com/mysteriumnetwork/node/identity"
	"github.com/mysteriumnetwork/node/identity/registry"
	"github.com/mysteriumnetwork/node/market"
	"github.com/mysteriumnetwork/node/mocks"
	"github.com/mysteriumnetwork/node/nat"
	natEvent "github.com/mysteriumnetwork/node/nat/event"
	nodeSession "github.com/mysteriumnetwork/node/session"
	sessionEvent "github.com/mysteriumnetwork/node/session/event"
	"github.com/mysteriumnetwork/node/session/pingpong"
	pingpongEvent "github.com/mysteriumnetwork/node/session/pingpong/event"
	"github.com/mysteriumnetwork/node/tequilapi/contract"
	"github.com/mysteriumnetwork/payments/crypto"
	"github.com/stretchr/testify/assert"
)

type debounceTester struct {
	numInteractions int
	lock            sync.Mutex
}

type interactionCounter interface {
	interactions() int
}

func (dt *debounceTester) do(interface{}) {
	dt.lock.Lock()
	dt.numInteractions++
	dt.lock.Unlock()
}

func (dt *debounceTester) interactions() int {
	dt.lock.Lock()
	defer dt.lock.Unlock()
	return dt.numInteractions
}

func Test_Debounce_CallsOnceInInterval(t *testing.T) {
	dt := &debounceTester{}
	duration := time.Millisecond * 10
	f := debounce(dt.do, duration)
	for i := 1; i < 10; i++ {
		f(struct{}{})
	}
	assert.Eventually(t, interacted(dt, 1), 2*time.Second, 10*time.Millisecond)
}

var mockNATStatus = nat.Status{
	Status: "status",
	Error:  errors.New("err"),
}

type natStatusProviderMock struct {
	statusToReturn  nat.Status
	numInteractions int
	lock            sync.Mutex
}

func (nspm *natStatusProviderMock) Status() nat.Status {
	nspm.lock.Lock()
	defer nspm.lock.Unlock()
	nspm.numInteractions++
	return nspm.statusToReturn
}

func (nspm *natStatusProviderMock) interactions() int {
	nspm.lock.Lock()
	defer nspm.lock.Unlock()
	return nspm.numInteractions
}

func (nspm *natStatusProviderMock) ConsumeNATEvent(event natEvent.Event) {}

type mockPublisher struct {
	lock           sync.Mutex
	publishedTopic string
	publishedData  interface{}
}

func (mp *mockPublisher) Publish(topic string, data interface{}) {
	mp.lock.Lock()
	defer mp.lock.Unlock()
	mp.publishedData = data
	mp.publishedTopic = topic
}

type serviceListerMock struct {
	lock             sync.Mutex
	numInteractions  int
	servicesToReturn map[service.ID]*service.Instance
}

func (slm *serviceListerMock) interactions() int {
	slm.lock.Lock()
	defer slm.lock.Unlock()
	return slm.numInteractions
}

func (slm *serviceListerMock) List() map[service.ID]*service.Instance {
	slm.lock.Lock()
	defer slm.lock.Unlock()
	slm.numInteractions++
	return slm.servicesToReturn
}

func Test_ConsumesNATEvents(t *testing.T) {
	natProvider := &natStatusProviderMock{
		statusToReturn: mockNATStatus,
	}
	publisher := &mockPublisher{}
	sl := &serviceListerMock{}

	duration := time.Millisecond * 3
	deps := KeeperDeps{
		NATStatusProvider: natProvider,
		Publisher:         publisher,
		ServiceLister:     sl,
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, duration)

	for i := 0; i < 5; i++ {
		// shoot a few events to see if we'll debounce
		keeper.consumeNATEvent(natEvent.Event{
			Stage:      "booster separation",
			Successful: false,
			Error:      errors.New("explosive bolts failed"),
		})
	}

	assert.Eventually(t, interacted(natProvider, 1), 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, natProvider.statusToReturn.Error.Error(), keeper.GetState().NATStatus.Error)
	assert.Equal(t, natProvider.statusToReturn.Status, keeper.GetState().NATStatus.Status)
}

func Test_ConsumesSessionEvents(t *testing.T) {
	// given
	expected := sessionEvent.SessionContext{
		ID:           "1",
		StartedAt:    time.Now(),
		ConsumerID:   identity.FromAddress("0x0000000000000000000000000000000000000001"),
		AccountantID: common.HexToAddress("0x000000000000000000000000000000000000000a"),
		Proposal: market.ServiceProposal{
			ServiceDefinition: &StubServiceDefinition{},
		},
	}

	eventBus := eventbus.New()
	deps := KeeperDeps{
		Publisher:        eventBus,
		IdentityProvider: &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	keeper.Subscribe(eventBus)

	// when
	eventBus.Publish(sessionEvent.AppTopicSession, sessionEvent.AppEventSession{
		Status:  sessionEvent.CreatedStatus,
		Session: expected,
	})

	// then
	assert.Eventually(t, func() bool {
		return len(keeper.GetState().Sessions) == 1
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(
		t,
		[]session.History{
			{
				SessionID:       nodeSession.ID(expected.ID),
				Direction:       session.DirectionProvided,
				ConsumerID:      expected.ConsumerID,
				AccountantID:    expected.AccountantID.Hex(),
				ProviderCountry: "MU",
				Started:         expected.StartedAt,
				Status:          session.StatusNew,
			},
		},
		keeper.GetState().Sessions,
	)

	// when
	eventBus.Publish(sessionEvent.AppTopicSession, sessionEvent.AppEventSession{
		Status:  sessionEvent.RemovedStatus,
		Session: expected,
	})

	// then
	assert.Eventually(t, func() bool {
		return len(keeper.GetState().Sessions) == 0
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_ConsumesSessionAcknowledgeEvents(t *testing.T) {
	// given
	myID := "test"
	expected := session.History{
		SessionID: nodeSession.ID("1"),
	}

	eventBus := eventbus.New()
	deps := KeeperDeps{
		Publisher:        eventBus,
		IdentityProvider: &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	keeper.Subscribe(eventBus)
	keeper.state.Services = []contract.ServiceInfoDTO{
		{ID: myID},
	}
	keeper.state.Sessions = []session.History{
		expected,
	}

	// when
	eventBus.Publish(sessionEvent.AppTopicSession, sessionEvent.AppEventSession{
		Status: sessionEvent.AcknowledgedStatus,
		Service: sessionEvent.ServiceContext{
			ID: myID,
		},
		Session: sessionEvent.SessionContext{
			ID: string(expected.SessionID),
		},
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Services[0].ConnectionStatistics.Successful == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_consumeServiceSessionEarningsEvent(t *testing.T) {
	// given
	eventBus := eventbus.New()
	deps := KeeperDeps{
		Publisher:        eventBus,
		IdentityProvider: &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	keeper.Subscribe(eventBus)
	keeper.state.Sessions = []session.History{
		{SessionID: nodeSession.ID("1")},
	}

	// when
	eventBus.Publish(sessionEvent.AppTopicTokensEarned, sessionEvent.AppEventTokensEarned{
		SessionID: "1",
		Total:     500,
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Sessions[0].Tokens != 0
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(
		t,
		[]session.History{
			{SessionID: nodeSession.ID("1"), Tokens: 500},
		},
		keeper.GetState().Sessions,
	)
}

func Test_consumeServiceSessionStatisticsEvent(t *testing.T) {
	// given
	eventBus := eventbus.New()
	deps := KeeperDeps{
		Publisher:        eventBus,
		IdentityProvider: &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	keeper.Subscribe(eventBus)
	keeper.state.Sessions = []session.History{
		{SessionID: nodeSession.ID("1")},
	}

	// when
	eventBus.Publish(sessionEvent.AppTopicDataTransferred, sessionEvent.AppEventDataTransferred{
		ID:   "1",
		Up:   1,
		Down: 2,
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Sessions[0].DataReceived != 0
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(
		t,
		[]session.History{
			{SessionID: nodeSession.ID("1"), DataSent: 2, DataReceived: 1},
		},
		keeper.GetState().Sessions,
	)
}

func Test_ConsumesServiceEvents(t *testing.T) {
	expected := service.Instance{}
	var id service.ID

	natProvider := &natStatusProviderMock{
		statusToReturn: mockNATStatus,
	}
	publisher := &mockPublisher{}
	sl := &serviceListerMock{
		servicesToReturn: map[service.ID]*service.Instance{
			id: &expected,
		},
	}

	duration := time.Millisecond * 3
	deps := KeeperDeps{
		NATStatusProvider: natProvider,
		Publisher:         publisher,
		ServiceLister:     sl,
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, duration)

	for i := 0; i < 5; i++ {
		// shoot a few events to see if we'll debounce
		keeper.consumeServiceStateEvent(servicestate.AppEventServiceStatus{})
	}

	assert.Eventually(t, interacted(sl, 1), 2*time.Second, 10*time.Millisecond)

	actual := keeper.GetState().Services[0]
	assert.Equal(t, string(id), actual.ID)
	assert.Equal(t, expected.Type, actual.Type)
	assert.Equal(t, expected.ProviderID.Address, actual.ProviderID)
	assert.Equal(t, expected.Options, actual.Options)
	assert.Equal(t, string(expected.State()), actual.Status)
	assert.EqualValues(t, contract.NewProposalDTO(expected.Proposal), actual.Proposal)
}

func Test_ConsumesConnectionStateEvents(t *testing.T) {
	// given
	expected := connection.Status{State: connection.Connected, SessionID: "1"}
	eventBus := eventbus.New()
	deps := KeeperDeps{
		NATStatusProvider: &natStatusProviderMock{statusToReturn: mockNATStatus},
		Publisher:         eventBus,
		ServiceLister:     &serviceListerMock{},
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	err := keeper.Subscribe(eventBus)
	assert.NoError(t, err)
	assert.Equal(t, connection.NotConnected, keeper.GetState().Connection.Session.State)

	// when
	eventBus.Publish(connection.AppTopicConnectionState, connection.AppEventConnectionState{
		State:       expected.State,
		SessionInfo: expected,
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Connection.Session.State == connection.Connected
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, expected, keeper.GetState().Connection.Session)
}

func Test_ConsumesConnectionStatisticsEvents(t *testing.T) {
	// given
	expected := connection.Statistics{
		At:            time.Now(),
		BytesReceived: 10 * datasize.MiB.Bytes(),
		BytesSent:     500 * datasize.KiB.Bytes(),
	}
	eventBus := eventbus.New()
	deps := KeeperDeps{
		NATStatusProvider: &natStatusProviderMock{statusToReturn: mockNATStatus},
		Publisher:         eventBus,
		ServiceLister:     &serviceListerMock{},
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	err := keeper.Subscribe(eventBus)
	assert.NoError(t, err)
	assert.True(t, keeper.GetState().Connection.Statistics.At.IsZero())

	// when
	eventBus.Publish(connection.AppTopicConnectionStatistics, connection.AppEventConnectionStatistics{
		Stats: expected,
	})

	// then
	assert.Eventually(t, func() bool {
		return expected == keeper.GetState().Connection.Statistics
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_ConsumesConnectionInvoiceEvents(t *testing.T) {
	// given
	expected := crypto.Invoice{
		AgreementID:    1,
		AgreementTotal: 1001,
		TransactorFee:  10,
	}
	eventBus := eventbus.New()
	deps := KeeperDeps{
		NATStatusProvider: &natStatusProviderMock{statusToReturn: mockNATStatus},
		Publisher:         eventBus,
		ServiceLister:     &serviceListerMock{},
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	err := keeper.Subscribe(eventBus)
	assert.NoError(t, err)
	assert.True(t, keeper.GetState().Connection.Statistics.At.IsZero())

	// when
	eventBus.Publish(pingpongEvent.AppTopicInvoicePaid, pingpongEvent.AppEventInvoicePaid{
		Invoice: expected,
	})

	// then
	assert.Eventually(t, func() bool {
		return expected == keeper.GetState().Connection.Invoice
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_ConsumesBalanceChangeEvent(t *testing.T) {
	// given
	eventBus := eventbus.New()
	deps := KeeperDeps{
		NATStatusProvider: &natStatusProviderMock{statusToReturn: mockNATStatus},
		Publisher:         eventBus,
		ServiceLister:     &serviceListerMock{},
		IdentityProvider: &mocks.IdentityProvider{
			Identities: []identity.Identity{
				{Address: "0x000000000000000000000000000000000000000a"},
			},
		},
		IdentityRegistry:          &mocks.IdentityRegistry{Status: registry.RegisteredConsumer},
		IdentityChannelCalculator: pingpong.NewChannelAddressCalculator("", "", ""),
		BalanceProvider:           &mockBalanceProvider{Balance: 0},
		EarningsProvider:          &mockEarningsProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	err := keeper.Subscribe(eventBus)
	assert.NoError(t, err)
	assert.Zero(t, keeper.GetState().Identities[0].Balance)

	// when
	eventBus.Publish(pingpongEvent.AppTopicBalanceChanged, pingpongEvent.AppEventBalanceChanged{
		Identity: identity.Identity{Address: "0x000000000000000000000000000000000000000a"},
		Previous: 0,
		Current:  999,
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Identities[0].Balance == 999
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_ConsumesEarningsChangeEvent(t *testing.T) {
	// given
	eventBus := eventbus.New()
	deps := KeeperDeps{
		NATStatusProvider: &natStatusProviderMock{statusToReturn: mockNATStatus},
		Publisher:         eventBus,
		ServiceLister:     &serviceListerMock{},
		IdentityProvider: &mocks.IdentityProvider{
			Identities: []identity.Identity{
				{Address: "0x000000000000000000000000000000000000000a"},
			},
		},
		IdentityRegistry:          &mocks.IdentityRegistry{Status: registry.RegisteredProvider},
		IdentityChannelCalculator: pingpong.NewChannelAddressCalculator("", "", ""),
		BalanceProvider:           &mockBalanceProvider{Balance: 0},
		EarningsProvider:          &mockEarningsProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	err := keeper.Subscribe(eventBus)
	assert.NoError(t, err)
	assert.Zero(t, keeper.GetState().Identities[0].Balance)

	// when
	eventBus.Publish(pingpongEvent.AppTopicEarningsChanged, pingpongEvent.AppEventEarningsChanged{
		Identity: identity.Identity{Address: "0x000000000000000000000000000000000000000a"},
		Previous: pingpongEvent.Earnings{},
		Current:  pingpongEvent.Earnings{LifetimeBalance: 100, UnsettledBalance: 10},
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Identities[0].Earnings == 10 && keeper.GetState().Identities[0].EarningsTotal == 100
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_ConsumesIdentityRegistrationEvent(t *testing.T) {
	// given
	eventBus := eventbus.New()
	deps := KeeperDeps{
		NATStatusProvider: &natStatusProviderMock{statusToReturn: mockNATStatus},
		Publisher:         eventBus,
		ServiceLister:     &serviceListerMock{},
		IdentityProvider: &mocks.IdentityProvider{
			Identities: []identity.Identity{
				{Address: "0x000000000000000000000000000000000000000a"},
			},
		},
		IdentityRegistry:          &mocks.IdentityRegistry{Status: registry.Unregistered},
		IdentityChannelCalculator: pingpong.NewChannelAddressCalculator("", "", ""),
		BalanceProvider:           &mockBalanceProvider{Balance: 0},
		EarningsProvider:          &mockEarningsProvider{},
	}
	keeper := NewKeeper(deps, time.Millisecond)
	err := keeper.Subscribe(eventBus)
	assert.NoError(t, err)
	assert.Equal(t, registry.Unregistered, keeper.GetState().Identities[0].RegistrationStatus)

	// when
	eventBus.Publish(registry.AppTopicIdentityRegistration, registry.AppEventIdentityRegistration{
		ID:     identity.Identity{Address: "0x000000000000000000000000000000000000000a"},
		Status: registry.RegisteredConsumer,
	})

	// then
	assert.Eventually(t, func() bool {
		return keeper.GetState().Identities[0].RegistrationStatus == registry.RegisteredConsumer
	}, 2*time.Second, 10*time.Millisecond)
}

func Test_getServiceByID(t *testing.T) {

	natProvider := &natStatusProviderMock{
		statusToReturn: mockNATStatus,
	}
	publisher := &mockPublisher{}
	sl := &serviceListerMock{
		servicesToReturn: map[service.ID]*service.Instance{},
	}

	duration := time.Millisecond * 3
	deps := KeeperDeps{
		NATStatusProvider: natProvider,
		Publisher:         publisher,
		ServiceLister:     sl,
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, duration)
	myID := "test"
	keeper.state.Services = []contract.ServiceInfoDTO{
		{ID: myID},
		{ID: "mock"},
	}

	s, found := keeper.getServiceByID(myID)
	assert.True(t, found)

	assert.EqualValues(t, keeper.state.Services[0], s)

	_, found = keeper.getServiceByID("something else")
	assert.False(t, found)
}

func Test_incrementConnectionCount(t *testing.T) {
	expected := service.Instance{}
	var id service.ID

	natProvider := &natStatusProviderMock{
		statusToReturn: mockNATStatus,
	}
	publisher := &mockPublisher{}
	sl := &serviceListerMock{
		servicesToReturn: map[service.ID]*service.Instance{
			id: &expected,
		},
	}

	duration := time.Millisecond * 3
	deps := KeeperDeps{
		NATStatusProvider: natProvider,
		Publisher:         publisher,
		ServiceLister:     sl,
		IdentityProvider:  &mocks.IdentityProvider{},
	}
	keeper := NewKeeper(deps, duration)
	myID := "test"
	keeper.state.Services = []contract.ServiceInfoDTO{
		{ID: myID},
		{ID: "mock"},
	}

	keeper.incrementConnectCount(myID, false)
	s, found := serviceByID(keeper.GetState().Services, myID)
	assert.True(t, found)

	assert.Equal(t, 1, s.ConnectionStatistics.Attempted)
	assert.Equal(t, 0, s.ConnectionStatistics.Successful)

	keeper.incrementConnectCount(myID, true)
	s, found = serviceByID(keeper.GetState().Services, myID)
	assert.True(t, found)

	assert.Equal(t, 1, s.ConnectionStatistics.Successful)
	assert.Equal(t, 1, s.ConnectionStatistics.Attempted)
}

func interacted(c interactionCounter, times int) func() bool {
	return func() bool {
		return c.interactions() == times
	}
}

type mockBalanceProvider struct {
	Balance uint64
}

// GetBalance returns a pre-defined balance.
func (mbp *mockBalanceProvider) GetBalance(_ identity.Identity) uint64 {
	return mbp.Balance
}

type mockEarningsProvider struct {
	Earnings pingpongEvent.Earnings
}

// GetEarnings returns a pre-defined settlement state.
func (mep *mockEarningsProvider) GetEarnings(_ identity.Identity) pingpongEvent.Earnings {
	return mep.Earnings
}

func serviceByID(services []contract.ServiceInfoDTO, id string) (se contract.ServiceInfoDTO, found bool) {
	for i := range services {
		if services[i].ID == id {
			se = services[i]
			found = true
			return
		}
	}
	return
}

type StubServiceDefinition struct{}

func (fs *StubServiceDefinition) GetLocation() market.Location {
	return market.Location{Country: "MU"}
}
