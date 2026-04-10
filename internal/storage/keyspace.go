package storage

// BuildInstanceKey 生成服务实例逻辑键。
func BuildInstanceKey(namespace, service, instanceID string) string {
	return "/" + namespace + "/" + service + "/" + instanceID
}
