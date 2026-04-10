package storage

// Batch 描述一组待批量应用的命令。
type Batch struct {
	Commands []Command
}
