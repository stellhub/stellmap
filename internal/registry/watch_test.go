package registry

import "testing"

func TestParseKey(t *testing.T) {
	namespace, service, instanceID, ok := ParseKey([]byte("/registry/prod/order-service/order-1"))
	if !ok {
		t.Fatalf("expected parse key success")
	}
	if namespace != "prod" || service != "order-service" || instanceID != "order-1" {
		t.Fatalf("unexpected parsed key: namespace=%s service=%s instanceID=%s", namespace, service, instanceID)
	}

	if _, _, _, ok := ParseKey([]byte("/kv/a")); ok {
		t.Fatalf("expected parse key failure for non-registry prefix")
	}
}

func TestWatchHubPublishAndUnsubscribe(t *testing.T) {
	hub := NewWatchHub()
	_, events, unsubscribe := hub.Subscribe(1)

	hub.Publish(WatchEvent{Revision: 1, Type: WatchEventUpsert, Namespace: "prod", Service: "order-service", InstanceID: "order-1"})

	event := <-events
	if event.Revision != 1 || event.Type != WatchEventUpsert || event.InstanceID != "order-1" {
		t.Fatalf("unexpected published event: %+v", event)
	}

	unsubscribe()
	if _, ok := <-events; ok {
		t.Fatalf("expected events channel closed after unsubscribe")
	}
}

func TestWatchHubSubscribeSinceReplaysRetainedEvents(t *testing.T) {
	hub := NewWatchHub()
	hub.Publish(WatchEvent{Revision: 1, Type: WatchEventUpsert, Namespace: "prod", Service: "order-service", InstanceID: "order-1"})
	hub.Publish(WatchEvent{Revision: 2, Type: WatchEventUpsert, Namespace: "prod", Service: "order-service", InstanceID: "order-2"})

	_, events, replay, exact, unsubscribe := hub.SubscribeSince(1, 1)
	defer unsubscribe()

	if !exact {
		t.Fatalf("expected retained history to cover since revision")
	}
	if len(replay) != 1 || replay[0].Revision != 2 {
		t.Fatalf("unexpected replay events: %+v", replay)
	}

	hub.Publish(WatchEvent{Revision: 3, Type: WatchEventDelete, Namespace: "prod", Service: "order-service", InstanceID: "order-2"})
	event := <-events
	if event.Revision != 3 || event.Type != WatchEventDelete {
		t.Fatalf("unexpected streamed event: %+v", event)
	}
}
