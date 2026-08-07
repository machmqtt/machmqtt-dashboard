package ws

import "testing"

// TestClientForceClosedAfterSustainedBroadcastDrops verifies that a client whose
// send buffer stays full across a sustained run of broadcasts is force-closed, so
// the browser can reconnect and resync instead of showing frozen data.
func TestClientForceClosedAfterSustainedBroadcastDrops(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())
	c.setEnv("e")
	pm := newPreparedMsg(t)

	// The first sendBufLen deliveries fill the buffer (each resetting the drop
	// streak); every delivery after that drops because nothing drains the buffer.
	for i := 0; i < sendBufLen+maxConsecutiveDrops; i++ {
		deliver(c, pm)
	}

	select {
	case <-c.done:
		// force-closed as expected
	default:
		t.Fatalf("client not force-closed after %d sustained drops", maxConsecutiveDrops)
	}
}

// TestClientDropStreakResetOnDelivery verifies a successful delivery resets the
// streak, so a client that occasionally lags is never force-closed.
func TestClientDropStreakResetOnDelivery(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())

	for i := 0; i < maxConsecutiveDrops-1; i++ {
		c.markDropped()
	}
	c.noteDelivered() // resets the streak
	c.markDropped()   // streak is now 1, far below the threshold

	select {
	case <-c.done:
		t.Fatal("client force-closed even though a delivery reset the drop streak")
	default:
	}
}

// TestClientTeardownIdempotent verifies teardown can be called repeatedly (read
// pump and write pump both call it) without panicking on a double channel close.
func TestClientTeardownIdempotent(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())

	c.teardown()
	c.teardown() // must not panic

	select {
	case <-c.done:
	default:
		t.Fatal("done channel not closed after teardown")
	}
}
