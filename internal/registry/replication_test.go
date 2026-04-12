package registry

import "testing"

func TestReplicatedKeyRoundTrip(t *testing.T) {
	key := ReplicatedKey("cn-bj", "bj-prod-01", "prod", "order-service", "order-1")

	sourceRegion, sourceClusterID, namespace, service, instanceID, ok := ParseReplicatedKey(key)
	if !ok {
		t.Fatalf("expected replicated key to parse")
	}
	if sourceRegion != "cn-bj" || sourceClusterID != "bj-prod-01" {
		t.Fatalf("unexpected source: %s %s", sourceRegion, sourceClusterID)
	}
	if namespace != "prod" || service != "order-service" || instanceID != "order-1" {
		t.Fatalf("unexpected target identity: %s %s %s", namespace, service, instanceID)
	}
}

func TestReplicationCheckpointKeyRoundTrip(t *testing.T) {
	key := ReplicationCheckpointKey("cn-sh", "sh-prod-01", "prod", "payment-service")

	sourceRegion, sourceClusterID, namespace, service, ok := ParseReplicationCheckpointKey(key)
	if !ok {
		t.Fatalf("expected checkpoint key to parse")
	}
	if sourceRegion != "cn-sh" || sourceClusterID != "sh-prod-01" {
		t.Fatalf("unexpected source: %s %s", sourceRegion, sourceClusterID)
	}
	if namespace != "prod" || service != "payment-service" {
		t.Fatalf("unexpected service identity: %s %s", namespace, service)
	}
}
