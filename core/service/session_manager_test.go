/*
 * Copyright (C) 2017 The "MysteriumNetwork/node" Authors.
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
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mysteriumnetwork/node/core/policy"
	"github.com/mysteriumnetwork/node/core/service/servicestate"
	"github.com/mysteriumnetwork/node/identity"
	"github.com/mysteriumnetwork/node/market"
	"github.com/mysteriumnetwork/node/mocks"
	"github.com/mysteriumnetwork/node/nat/event"
	"github.com/mysteriumnetwork/node/p2p"
	"github.com/mysteriumnetwork/node/pb"
	sessionEvent "github.com/mysteriumnetwork/node/session/event"
	"github.com/mysteriumnetwork/node/trace"
	"github.com/stretchr/testify/assert"
)

var (
	currentProposalID = 68
	currentProposal   = market.ServiceProposal{
		ServiceType: "mockservice",
		ID:          currentProposalID,
	}
	currentService = NewInstance(
		identity.FromAddress(currentProposal.ProviderID),
		currentProposal.ServiceType,
		struct{}{},
		currentProposal,
		servicestate.Running,
		&mockService{},
		policy.NewRepository(),
		&mockDiscovery{},
	)
	consumerID   = identity.FromAddress("deadbeef")
	accountantID = common.HexToAddress("0x1")
)

type mockBalanceTracker struct {
	paymentError      error
	firstPaymentError error
}

func (m mockBalanceTracker) Start() error {
	return m.paymentError
}

func (m mockBalanceTracker) Stop() {

}

func (m mockBalanceTracker) WaitFirstInvoice(time.Duration) error {
	return m.firstPaymentError
}

type mockP2PChannel struct{}

func (m *mockP2PChannel) Send(_ context.Context, _ string, _ *p2p.Message) (*p2p.Message, error) {
	return nil, nil
}

func (m *mockP2PChannel) Handle(topic string, handler p2p.HandlerFunc) {
}

func (m *mockP2PChannel) ServiceConn() *net.UDPConn { return nil }

func (m *mockP2PChannel) Conn() *net.UDPConn { return nil }

func (m *mockP2PChannel) Close() error { return nil }

func TestManager_Start_StoresSession(t *testing.T) {
	publisher := mocks.NewEventBus()
	sessionStore := NewSessionPool(publisher)
	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{})

	_, err := manager.Start(&pb.SessionRequest{
		Consumer: &pb.ConsumerInfo{
			Id:           consumerID.Address,
			AccountantID: accountantID.String(),
		},
		ProposalID: int64(currentProposalID),
	})
	assert.NoError(t, err)

	session := sessionStore.GetAll()[0]
	assert.Equal(t, consumerID, session.ConsumerID)

	assert.Eventually(t, func() bool {
		history := publisher.GetEventHistory()
		if len(history) != 5 {
			return false
		}

		assert.Equal(t, sessionEvent.AppTopicSession, history[0].Topic)
		startEvent := history[0].Event.(sessionEvent.AppEventSession)
		assert.Equal(t, sessionEvent.CreatedStatus, startEvent.Status)
		assert.Equal(t, consumerID, startEvent.Session.ConsumerID)
		assert.Equal(t, accountantID, startEvent.Session.AccountantID)
		assert.Equal(t, currentProposal, startEvent.Session.Proposal)

		assert.Equal(t, trace.AppTopicTraceEvent, history[1].Topic)
		traceEvent1 := history[1].Event.(trace.Event)
		assert.Equal(t, "Provider whole session create", traceEvent1.Key)

		assert.Equal(t, trace.AppTopicTraceEvent, history[2].Topic)
		traceEvent2 := history[2].Event.(trace.Event)
		assert.Equal(t, "Provider session start", traceEvent2.Key)

		assert.Equal(t, trace.AppTopicTraceEvent, history[3].Topic)
		traceEvent3 := history[3].Event.(trace.Event)
		assert.Equal(t, "Provider payments", traceEvent3.Key)

		assert.Equal(t, trace.AppTopicTraceEvent, history[4].Topic)
		traceEvent4 := history[4].Event.(trace.Event)
		assert.Equal(t, "Provider config", traceEvent4.Key)

		return true
	}, time.Second, 10*time.Millisecond)
}

func TestManager_Start_DisconnectsOnPaymentError(t *testing.T) {
	publisher := mocks.NewEventBus()
	sessionStore := NewSessionPool(publisher)
	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{
		firstPaymentError: errors.New("sorry, your money ended"),
	})

	_, err := manager.Start(&pb.SessionRequest{
		Consumer: &pb.ConsumerInfo{
			Id:           consumerID.Address,
			AccountantID: accountantID.String(),
		},
		ProposalID: int64(currentProposalID),
	})
	assert.EqualError(t, err, "first invoice was not paid: sorry, your money ended")
	assert.Eventually(t, func() bool {
		history := publisher.GetEventHistory()
		if len(history) != 5 {
			return false
		}

		assert.Equal(t, sessionEvent.AppTopicSession, history[0].Topic)
		startEvent := history[0].Event.(sessionEvent.AppEventSession)
		assert.Equal(t, sessionEvent.CreatedStatus, startEvent.Status)
		assert.Equal(t, consumerID, startEvent.Session.ConsumerID)
		assert.Equal(t, accountantID, startEvent.Session.AccountantID)
		assert.Equal(t, currentProposal, startEvent.Session.Proposal)

		assert.Equal(t, trace.AppTopicTraceEvent, history[1].Topic)
		traceEvent1 := history[1].Event.(trace.Event)
		assert.Equal(t, "Provider whole session create", traceEvent1.Key)

		assert.Equal(t, trace.AppTopicTraceEvent, history[2].Topic)
		traceEvent2 := history[2].Event.(trace.Event)
		assert.Equal(t, "Provider session start", traceEvent2.Key)

		assert.Equal(t, trace.AppTopicTraceEvent, history[3].Topic)
		traceEvent3 := history[3].Event.(trace.Event)
		assert.Equal(t, "Provider payments", traceEvent3.Key)

		assert.Equal(t, sessionEvent.AppTopicSession, history[4].Topic)
		closeEvent := history[4].Event.(sessionEvent.AppEventSession)
		assert.Equal(t, sessionEvent.RemovedStatus, closeEvent.Status)
		assert.Equal(t, consumerID, closeEvent.Session.ConsumerID)
		assert.Equal(t, accountantID, closeEvent.Session.AccountantID)
		assert.Equal(t, currentProposal, closeEvent.Session.Proposal)

		return true
	}, time.Second, 10*time.Millisecond)
}

func TestManager_Start_Second_Session_Destroy_Stale_Session(t *testing.T) {
	sessionRequest := &pb.SessionRequest{
		Consumer: &pb.ConsumerInfo{
			Id:           consumerID.Address,
			AccountantID: accountantID.String(),
		},
		ProposalID: int64(currentProposalID),
	}

	publisher := mocks.NewEventBus()
	sessionStore := NewSessionPool(publisher)
	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{})

	_, err := manager.Start(sessionRequest)
	assert.NoError(t, err)

	sessionOld := sessionStore.GetAll()[0]
	assert.Equal(t, consumerID, sessionOld.ConsumerID)

	_, err = manager.Start(sessionRequest)
	assert.NoError(t, err)

	assert.NoError(t, err)
	assert.Eventuallyf(t, func() bool {
		_, found := sessionStore.Find(sessionOld.ID)
		return !found
	}, time.Second, 10*time.Millisecond, "Waiting for session destroy")
}

func TestManager_Start_RejectsUnknownProposal(t *testing.T) {
	publisher := mocks.NewEventBus()
	sessionStore := NewSessionPool(mocks.NewEventBus())
	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{})

	_, err := manager.Start(&pb.SessionRequest{
		Consumer: &pb.ConsumerInfo{
			Id:           consumerID.Address,
			AccountantID: accountantID.String(),
		},
		ProposalID: int64(69),
	})

	assert.Exactly(t, err, ErrorInvalidProposal)
	assert.Len(t, sessionStore.GetAll(), 0)
	assert.Eventually(t, func() bool {
		history := publisher.GetEventHistory()
		if len(history) != 2 {
			return false
		}

		assert.Equal(t, trace.AppTopicTraceEvent, history[0].Topic)
		traceEvent1 := history[0].Event.(trace.Event)
		assert.Equal(t, "Provider whole session create", traceEvent1.Key)

		assert.Equal(t, trace.AppTopicTraceEvent, history[1].Topic)
		traceEvent2 := history[1].Event.(trace.Event)
		assert.Equal(t, "Provider session start", traceEvent2.Key)

		return true
	}, time.Second, 10*time.Millisecond)
}

type MockNatEventTracker struct {
}

func (mnet *MockNatEventTracker) LastEvent() *event.Event {
	return &event.Event{}
}

func TestManager_AcknowledgeSession_RejectsUnknown(t *testing.T) {
	publisher := mocks.NewEventBus()
	sessionStore := NewSessionPool(publisher)
	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{})

	err := manager.Acknowledge(consumerID, "")
	assert.Exactly(t, err, ErrorSessionNotExists)
}

func TestManager_AcknowledgeSession_RejectsBadClient(t *testing.T) {
	publisher := mocks.NewEventBus()
	sessionStore := NewSessionPool(mocks.NewEventBus())
	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{})

	session, err := manager.Start(&pb.SessionRequest{
		Consumer: &pb.ConsumerInfo{
			Id:           consumerID.Address,
			AccountantID: accountantID.String(),
		},
		ProposalID: int64(currentProposalID),
	})
	assert.Nil(t, err)

	err = manager.Acknowledge(identity.FromAddress("some other id"), string(session.ID))
	assert.Exactly(t, ErrorWrongSessionOwner, err)
}

func TestManager_AcknowledgeSession_PublishesEvent(t *testing.T) {
	publisher := mocks.NewEventBus()

	sessionStore := NewSessionPool(publisher)
	session := &Session{ID: "1", ConsumerID: consumerID}
	sessionStore.Add(session)

	manager := newManager(currentService, sessionStore, publisher, &mockBalanceTracker{})

	err := manager.Acknowledge(consumerID, string(session.ID))
	assert.Nil(t, err)
	assert.Eventually(t, func() bool {
		// Check that state event with StateIPNotChanged status was called.
		history := publisher.GetEventHistory()
		for _, v := range history {
			if v.Topic == sessionEvent.AppTopicSession && v.Event.(sessionEvent.AppEventSession).Status == sessionEvent.AcknowledgedStatus {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
}

func newManager(service *Instance, sessions *SessionPool, publisher publisher, paymentEngine PaymentEngine) *SessionManager {
	return NewSessionManager(
		service,
		sessions,
		func(_, _ identity.Identity, _ common.Address, _ string) (PaymentEngine, error) {
			return paymentEngine, nil
		},
		&MockNatEventTracker{},
		publisher,
		&mockP2PChannel{},
		DefaultConfig(),
	)
}
