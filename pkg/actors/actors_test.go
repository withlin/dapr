// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dapr/components-contrib/state"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
)

const (
	TestDaprID = "fakeDaprID"
)

type fakeStateStore struct {
	items map[string][]byte
	lock  *sync.RWMutex
}

func (f *fakeStateStore) Init(metadata state.Metadata) error {
	return nil
}

func (f *fakeStateStore) Delete(req *state.DeleteRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.items, req.Key)
	return nil
}

func (f *fakeStateStore) BulkDelete(req []state.DeleteRequest) error {
	return nil
}

func (f *fakeStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	item := f.items[req.Key]
	return &state.GetResponse{Data: item}, nil
}

func (f *fakeStateStore) Set(req *state.SetRequest) error {
	b, _ := json.Marshal(&req.Value)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items[req.Key] = b
	return nil
}

func (f *fakeStateStore) BulkSet(req []state.SetRequest) error {
	return nil
}

func (f *fakeStateStore) Multi(reqs []state.TransactionalRequest) error {
	return nil
}

func newTestActorsRuntime() *actorsRuntime {
	mockAppChannel := new(channelt.MockAppChannel)
	fakeHTTPResponse := &channel.InvokeResponse{
		Metadata: map[string]string{http.HTTPStatusCode: "200"},
	}
	mockAppChannel.On(
		"InvokeMethod",
		mock.AnythingOfType("*channel.InvokeRequest")).Return(fakeHTTPResponse, nil)

	store := fakeStore()
	config := NewConfig("", TestDaprID, "", nil, 0, "", "", "", false)
	a := NewActors(store, mockAppChannel, nil, config)

	return a.(*actorsRuntime)
}

func getTestActorTypeAndID() (string, string) {
	return "cat", "hobbit"
}

func fakeStore() state.StateStore {
	return &fakeStateStore{
		items: map[string][]byte{},
		lock:  &sync.RWMutex{},
	}
}

func fakeCallAndActivateActor(actors *actorsRuntime, actorKey string) {
	actors.actorsTable.LoadOrStore(actorKey, &actor{
		lastUsedTime: time.Now(),
		lock:         &sync.RWMutex{},
		busy:         false,
		busyCh:       make(chan bool, 1),
	})
}

func deactivateActorWithDuration(testActorsRuntime *actorsRuntime, actorKey string, actorIdleTimeout time.Duration) {
	fakeCallAndActivateActor(testActorsRuntime, actorKey)
	scanInterval := time.Second * 1
	testActorsRuntime.startDeactivationTicker(scanInterval, actorIdleTimeout)
}

func createReminder(actorID, actorType, name, period, dueTime, data string) CreateReminderRequest {
	return CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		Data:      data,
	}
}

func createTimer(actorID, actorType, name, period, dueTime, callback, data string) CreateTimerRequest {
	return CreateTimerRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      name,
		Period:    period,
		DueTime:   dueTime,
		Data:      data,
		Callback:  callback,
	}
}

func TestActorIsDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	idleTimeout := time.Second * 2
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorKey, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	assert.False(t, exists)
}

func TestActorIsNotDeactivated(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	idleTimeout := time.Second * 5
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)

	deactivateActorWithDuration(testActorsRuntime, actorKey, idleTimeout)
	time.Sleep(time.Second * 3)

	_, exists := testActorsRuntime.actorsTable.Load(actorKey)

	assert.True(t, exists)
}

func TestTimerExecution(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	fakeCallAndActivateActor(testActorsRuntime, actorKey)

	err := testActorsRuntime.executeTimer(actorType, actorID, "timer1", "2s", "2s", "callback", "data")
	assert.Nil(t, err)
}

func TestReminderExecution(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	fakeCallAndActivateActor(testActorsRuntime, actorKey)

	err := testActorsRuntime.executeReminder(actorType, actorID, "2s", "2s", "reminder1", "data")
	assert.Nil(t, err)
}

func TestSetReminderTrack(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	err := testActorsRuntime.updateReminderTrack(actorType, actorID)
	assert.Nil(t, err)
}

func TestGetReminderTrack(t *testing.T) {
	t.Run("reminder doesn't exist", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		r, _ := testActorsRuntime.getReminderTrack(actorType, actorID)
		assert.Empty(t, r.LastFiredTime)
	})

	t.Run("reminder exists", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		testActorsRuntime.updateReminderTrack(actorType, actorID)
		r, _ := testActorsRuntime.getReminderTrack(actorType, actorID)
		assert.NotEmpty(t, r.LastFiredTime)
	})
}

func TestCreateReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	err := testActorsRuntime.CreateReminder(&CreateReminderRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Name:      "reminder1",
		Period:    "1s",
		DueTime:   "1s",
		Data:      nil,
	})
	assert.Nil(t, err)
}

func TestOverrideReminder(t *testing.T) {
	t.Run("override data", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminder(actorID, actorType, "reminder1", "1s", "1s", "a")
		err := testActorsRuntime.CreateReminder(&reminder)
		assert.Nil(t, err)

		reminder2 := createReminder(actorID, actorType, "reminder1", "1s", "1s", "b")
		testActorsRuntime.CreateReminder(&reminder2)
		reminders, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "b", reminders[0].Data)
	})

	t.Run("override dueTime", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminder(actorID, actorType, "reminder1", "1s", "1s", "")
		err := testActorsRuntime.CreateReminder(&reminder)
		assert.Nil(t, err)

		reminder2 := createReminder(actorID, actorType, "reminder1", "1s", "2s", "")
		testActorsRuntime.CreateReminder(&reminder2)
		reminders, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].DueTime)
	})

	t.Run("override period", func(t *testing.T) {
		testActorsRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()
		reminder := createReminder(actorID, actorType, "reminder1", "1s", "1s", "")
		err := testActorsRuntime.CreateReminder(&reminder)
		assert.Nil(t, err)

		reminder2 := createReminder(actorID, actorType, "reminder1", "2s", "1s", "")
		testActorsRuntime.CreateReminder(&reminder2)
		reminders, err := testActorsRuntime.getRemindersForActorType(actorType)
		assert.Nil(t, err)
		assert.Equal(t, "2s", reminders[0].Period)
	})
}

func TestDeleteReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	reminder := createReminder(actorID, actorType, "reminder1", "1s", "1s", "")
	testActorsRuntime.CreateReminder(&reminder)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))
	err := testActorsRuntime.DeleteReminder(&DeleteReminderRequest{
		Name:      "reminder1",
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.Nil(t, err)
	assert.Equal(t, 0, len(testActorsRuntime.reminders[actorType]))
}

func TestGetReminder(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	reminder := createReminder(actorID, actorType, "reminder1", "1s", "1s", "a")
	testActorsRuntime.CreateReminder(&reminder)
	assert.Equal(t, 1, len(testActorsRuntime.reminders[actorType]))
	r, err := testActorsRuntime.GetReminder(&GetReminderRequest{
		Name:      "reminder1",
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.Nil(t, err)
	assert.Equal(t, r.Data, "a")
	assert.Equal(t, r.Period, "1s")
	assert.Equal(t, r.DueTime, "1s")
}

func TestDeleteTimer(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	fakeCallAndActivateActor(testActorsRuntime, actorKey)

	timer := createTimer(actorID, actorType, "timer1", "100ms", "100ms", "callback", "")
	err := testActorsRuntime.CreateTimer(&timer)
	assert.Nil(t, err)

	timerKey := fmt.Sprintf("%s-%s", actorKey, timer.Name)

	_, ok := testActorsRuntime.activeTimers.Load(timerKey)
	assert.True(t, ok)

	err = testActorsRuntime.DeleteTimer(&DeleteTimerRequest{
		Name:      timer.Name,
		ActorID:   actorID,
		ActorType: actorType,
	})
	assert.Nil(t, err)

	_, ok = testActorsRuntime.activeTimers.Load(timerKey)
	assert.False(t, ok)
}

func TestReminderFires(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	reminder := createReminder(actorID, actorType, "reminder1", "100ms", "100ms", "a")
	err := testActorsRuntime.CreateReminder(&reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 250)
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotNil(t, track)
	assert.NotEmpty(t, track.LastFiredTime)
}

func TestReminderDueDate(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	reminder := createReminder(actorID, actorType, "reminder1", "100ms", "500ms", "a")
	err := testActorsRuntime.CreateReminder(&reminder)
	assert.Nil(t, err)

	track, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.Empty(t, track.LastFiredTime)

	time.Sleep(time.Second * 1)

	track, err = testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotEmpty(t, track.LastFiredTime)
}

func TestReminderPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	reminder := createReminder(actorID, actorType, "reminder1", "100ms", "100ms", "a")
	err := testActorsRuntime.CreateReminder(&reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 250)

	track, _ := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.NotEmpty(t, track.LastFiredTime)

	time.Sleep(time.Second * 3)

	track2, err := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Nil(t, err)
	assert.NotEmpty(t, track2.LastFiredTime)

	assert.NotEqual(t, track.LastFiredTime, track2.LastFiredTime)
}

func TestReminderFiresOnceWitnEmptyPeriod(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	actorKey := testActorsRuntime.constructCombinedActorKey(actorType, actorID)
	reminder := createReminder(actorID, actorType, "reminder1", "", "100ms", "a")
	err := testActorsRuntime.CreateReminder(&reminder)
	assert.Nil(t, err)

	time.Sleep(time.Millisecond * 150)

	track, _ := testActorsRuntime.getReminderTrack(actorKey, "reminder1")
	assert.Empty(t, track.LastFiredTime)
}

func TestConstructActorStateKey(t *testing.T) {
	testActorsRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	keyName := "key0"
	expected := fmt.Sprintf("%s-%s-%s-%s", TestDaprID, actorType, actorID, keyName)

	// act
	stateKey := testActorsRuntime.constructActorStateKey(actorType, actorID, keyName)

	// assert
	assert.Equal(t, expected, stateKey)
}

func TestSaveState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	keyName := "key0"
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	// act
	actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
	fakeCallAndActivateActor(testActorRuntime, actorKey)

	err := testActorRuntime.SaveState(&SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
		Value:     val,
	})
	assert.NoError(t, err)

	// assert
	response, err := testActorRuntime.GetState(&GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
	})

	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))
}

func TestGetState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	keyName := "key0"
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
	fakeCallAndActivateActor(testActorRuntime, actorKey)

	testActorRuntime.SaveState(&SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
		Value:     val,
	})

	// act
	response, err := testActorRuntime.GetState(&GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
	})

	// assert
	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))
}

func TestDeleteState(t *testing.T) {
	testActorRuntime := newTestActorsRuntime()
	actorType, actorID := getTestActorTypeAndID()
	keyName := "key0"
	fakeData := strconv.Quote("fakeData")

	var val interface{}
	jsoniter.ConfigFastest.Unmarshal([]byte(fakeData), &val)

	// save test state
	actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
	fakeCallAndActivateActor(testActorRuntime, actorKey)

	testActorRuntime.SaveState(&SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
		Value:     val,
	})

	// make sure that state is stored.
	response, err := testActorRuntime.GetState(&GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
	})

	assert.NoError(t, err)
	assert.Equal(t, fakeData, string(response.Data))

	// act
	err = testActorRuntime.DeleteState(&DeleteStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
	})
	assert.NoError(t, err)

	// assert
	response, err = testActorRuntime.GetState(&GetStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       keyName,
	})

	assert.NoError(t, err)
	assert.Nil(t, response.Data)
}

func TestTransactionalState(t *testing.T) {
	t.Run("Single set request succeeds", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
		fakeCallAndActivateActor(testActorRuntime, actorKey)

		err := testActorRuntime.TransactionalStateOperation(&TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: Upsert,
					Request: TransactionalUpsert{
						Key:   "key1",
						Value: "fakeData",
					},
				},
			},
		})
		assert.Nil(t, err)
	})

	t.Run("Multiple requests succeeds", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
		fakeCallAndActivateActor(testActorRuntime, actorKey)

		err := testActorRuntime.TransactionalStateOperation(&TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: Upsert,
					Request: TransactionalUpsert{
						Key:   "key1",
						Value: "fakeData",
					},
				},
				{
					Operation: Delete,
					Request: TransactionalDelete{
						Key: "key1",
					},
				},
			},
		})
		assert.Nil(t, err)
	})

	t.Run("Wrong request body - should fail", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
		fakeCallAndActivateActor(testActorRuntime, actorKey)

		err := testActorRuntime.TransactionalStateOperation(&TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: Upsert,
					Request:   "wrongBody",
				},
			},
		})
		assert.NotNil(t, err)
	})

	t.Run("Unsupported operation type - should fail", func(t *testing.T) {
		testActorRuntime := newTestActorsRuntime()
		actorType, actorID := getTestActorTypeAndID()

		actorKey := testActorRuntime.constructCombinedActorKey(actorType, actorID)
		fakeCallAndActivateActor(testActorRuntime, actorKey)

		err := testActorRuntime.TransactionalStateOperation(&TransactionalRequest{
			ActorType: actorType,
			ActorID:   actorID,
			Operations: []TransactionalOperation{
				{
					Operation: "Wrong",
					Request:   "wrongBody",
				},
			},
		})
		assert.NotNil(t, err)
		assert.Equal(t, "operation type Wrong not supported", err.Error())
	})
}
