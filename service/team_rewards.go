package service

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var teamRewardsOnce sync.Once

// The database transaction, not the timer, deduplicates grants across masters.
func StartTeamRewardsTask() {
	teamRewardsOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				if err := model.ProcessPendingAgentCommissions(100); err != nil {
					common.SysError("team rewards settlement: " + err.Error())
				}
				<-ticker.C
			}
		}()
	})
}
