package main

import (
	"crypto/ecdsa"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/seal"
)

// newSealer 返回引擎用的封存队列工厂（US10）。
//
// 引擎只定义 play.SealQueue 接口；真正的封存循环在 internal/seal，
// 这样 internal/play 不必依赖封存实现的细节，桌面外壳也可以换成别的实现。
func newSealer() play.SealFactory {
	return func(jobs []seal.Job, pub *ecdsa.PublicKey) play.SealQueue {
		if len(jobs) == 0 || pub == nil {
			return nil
		}
		q := seal.NewQueue(jobs, seal.Options{
			// 不覆盖已有密档；被取消/失败的半成品自行清理（其中不含明文）。
			Overwrite:              false,
			RemovePartialOnFailure: true,
		})
		q.Start(pub)
		return q
	}
}
