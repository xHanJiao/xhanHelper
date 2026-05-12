package idgen

import (
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
)

var (
	node *snowflake.Node
	once sync.Once
)

// Init 初始化 Snowflake 节点
func Init(nodeID int64) error {
	var err error
	once.Do(func() {
		node, err = snowflake.NewNode(nodeID)
		if err != nil {
			log.Printf("[idgen] init failed: node=%d err=%v", nodeID, err)
			return
		}
		log.Printf("[idgen] initialized: node=%d", nodeID)
	})
	return err
}

// Next 生成下一个 ID
func Next() string {
	if node == nil {
		log.Printf("[idgen] warning: not initialized")
		panic("idgen not initialized")
	}
	return node.Generate().String()
}
