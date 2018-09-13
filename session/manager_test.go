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

package session

import (
	"testing"

	"github.com/mysteriumnetwork/node/identity"
	"github.com/stretchr/testify/assert"
)

var (
	expectedID      = ID("mocked-id")
	expectedSession = Session{
		ID:         expectedID,
		Config:     expectedSessionConfig,
		ConsumerID: identity.FromAddress("deadbeef"),
	}
	lastSession Session
)

const expectedSessionConfig = "config_string"

func mockedConfigProvider() (ServiceConfiguration, error) {
	return expectedSessionConfig, nil
}

func generateSessionID() ID {
	return expectedID
}

func saveSession(sessionInstance Session) {
	lastSession = sessionInstance
}

type fakePromiseProcessor struct {
	started bool
}

func (processor *fakePromiseProcessor) Start() error {
	processor.started = true
	return nil
}

func (processor *fakePromiseProcessor) Stop() error {
	processor.started = false
	return nil
}

func TestManager_Create_StoresSession(t *testing.T) {
	manager := NewManager(generateSessionID, mockedConfigProvider, saveSession, &fakePromiseProcessor{})

	sessionInstance, err := manager.Create(identity.FromAddress("deadbeef"))
	assert.NoError(t, err)
	assert.Exactly(t, expectedSession, sessionInstance)
	assert.Exactly(t, expectedSession, lastSession)
}

func TestManager_Create_StartsPromiseProcessor(t *testing.T) {
	promiseProcessor := &fakePromiseProcessor{}
	manager := NewManager(generateSessionID, mockedConfigProvider, saveSession, promiseProcessor)

	_, err := manager.Create(identity.FromAddress("deadbeef"))
	assert.NoError(t, err)
	assert.True(t, promiseProcessor.started)

}
