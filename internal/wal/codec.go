package wal

import raftpb "go.etcd.io/raft/v3/raftpb"

// Codec 定义 WAL 记录的编解码能力。
//
// 这里单独抽一个接口，而不是把 protobuf 编码直接写死在 FileWAL 里，
// 主要是为了把“日志如何落盘”与“日志如何序列化”解耦：
// 1. FileWAL 只关心 segment 切分、追加、截断和恢复顺序。
// 2. Codec 只关心 HardState / Entry 如何转成字节。
//
// 当前实现只提供 protobuf 版本，但后续如果要替换成自定义二进制格式、
// 增加校验头或压缩头，修改点会被收敛在这里，而不是扩散到 WAL 主流程里。
type Codec interface {
	EncodeState(state raftpb.HardState) ([]byte, error)
	DecodeState(data []byte) (raftpb.HardState, error)
	EncodeEntry(entry raftpb.Entry) ([]byte, error)
	DecodeEntry(data []byte) (raftpb.Entry, error)
}

// ProtobufCodec 使用 raftpb 自带的 protobuf 编解码能力持久化 WAL 记录。
//
// 之所以优先采用 protobuf，而不是额外定义一套磁盘格式，是因为：
// 1. raftpb.Entry / HardState 本身已经是稳定的 protobuf 消息。
// 2. 可以减少一次结构转换，避免编码语义偏差。
// 3. 当前阶段优先保证恢复正确性和实现简洁，而不是追求自定义格式优化。
type ProtobufCodec struct{}

// EncodeState 编码 HardState。
//
// HardState 记录的是必须持久化的 Raft 核心状态，例如当前任期、投票对象和提交索引。
// 在 FileWAL 中，它会单独落到 stateFile，而不是与日志条目混在一起。
func (ProtobufCodec) EncodeState(state raftpb.HardState) ([]byte, error) {
	return state.Marshal()
}

// DecodeState 解码 HardState。
//
// 它会在 WAL Open / 恢复阶段被调用，用来恢复本地节点最近一次已持久化的 HardState。
func (ProtobufCodec) DecodeState(data []byte) (raftpb.HardState, error) {
	var state raftpb.HardState
	err := state.Unmarshal(data)
	return state, err
}

// EncodeEntry 编码日志条目。
//
// Entry 是 Raft Log 的最小复制单元，后续会被 FileWAL 写成：
// length prefix + protobuf payload
// 这样的记录格式，便于顺序扫描和按条目恢复。
func (ProtobufCodec) EncodeEntry(entry raftpb.Entry) ([]byte, error) {
	return entry.Marshal()
}

// DecodeEntry 解码日志条目。
//
// 它主要在 WAL 启动恢复和 segment 重放时被调用，用来把磁盘记录重新还原成 raftpb.Entry。
func (ProtobufCodec) DecodeEntry(data []byte) (raftpb.Entry, error) {
	var entry raftpb.Entry
	err := entry.Unmarshal(data)
	return entry, err
}
