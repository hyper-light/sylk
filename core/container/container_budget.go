package container

import (
	"math"
	"time"
)

func (q *ResourceQuota) adjustContainerReleaseCreditLocked(now time.Time, delta float64) {
	credit := q.decayContainerReleaseCreditLocked(now) + delta
	if credit < 0 {
		credit = 0
	}
	q.containerReleaseCredit = credit
	q.containerLastEvent = now
}

func (q *ResourceQuota) decayContainerReleaseCreditLocked(now time.Time) float64 {
	if q.containerReleaseCredit <= 0 {
		if q.containerLastEvent.IsZero() {
			q.containerLastEvent = now
		}
		return 0
	}
	if q.containerLastEvent.IsZero() {
		q.containerLastEvent = now
		return q.containerReleaseCredit
	}
	if q.containerReleaseHalfLife <= 0 {
		return q.containerReleaseCredit
	}
	elapsed := now.Sub(q.containerLastEvent)
	if elapsed <= 0 {
		return q.containerReleaseCredit
	}
	q.containerReleaseCredit *= math.Exp(-0.693 * elapsed.Seconds() / q.containerReleaseHalfLife.Seconds())
	if q.containerReleaseCredit < 0.01 {
		q.containerReleaseCredit = 0
	}
	q.containerLastEvent = now
	return q.containerReleaseCredit
}

func (q *ResourceQuota) containerCapacitiesLocked(now time.Time, candidate *ContainerSpec) (soft, burst, hard int64) {
	base := max(q.containerLimit, 0)
	if base == 0 {
		return 0, 0, 0
	}

	headroom := q.containerElasticHeadroomLocked(candidate)
	releaseCredit := int64(math.Ceil(q.decayContainerReleaseCreditLocked(now)))
	if releaseCredit < 0 {
		releaseCredit = 0
	}
	if releaseCredit > headroom {
		releaseCredit = headroom
	}

	soft = base
	burst = base + releaseCredit
	hard = base + headroom
	if burst < soft {
		burst = soft
	}
	if hard < burst {
		hard = burst
	}
	return soft, burst, hard
}

func (q *ResourceQuota) containerElasticHeadroomLocked(candidate *ContainerSpec) int64 {
	if q == nil {
		return 0
	}

	headroom := int64(-1)
	if q.goroutineLimit > 0 {
		perContainer := q.estimatedPerContainerGoroutinesLocked(candidate)
		if perContainer > 0 {
			free := q.goroutineLimit - q.goroutineUsed.Load()
			if free < 0 {
				free = 0
			}
			headroom = free / perContainer
		}
	}
	if q.vfsQuotaLimit > 0 {
		perContainer := q.estimatedPerContainerVFSLocked(candidate)
		if perContainer > 0 {
			free := q.vfsQuotaLimit - q.vfsQuotaUsed.Load()
			if free < 0 {
				free = 0
			}
			vfsHeadroom := free / perContainer
			if headroom < 0 || vfsHeadroom < headroom {
				headroom = vfsHeadroom
			}
		}
	}
	if headroom < 0 {
		return 0
	}
	return headroom
}

func (q *ResourceQuota) estimatedPerContainerGoroutinesLocked(candidate *ContainerSpec) int64 {
	current := q.containerCount.Load()
	used := q.goroutineUsed.Load()
	estimate := int64(0)
	if current > 0 && used > 0 {
		estimate = used / current
		if used%current != 0 {
			estimate++
		}
	}
	if candidate != nil && candidate.Resources.GoroutineLimit > estimate {
		estimate = candidate.Resources.GoroutineLimit
	}
	if estimate <= 0 {
		estimate = 1
	}
	return estimate
}

func (q *ResourceQuota) estimatedPerContainerVFSLocked(candidate *ContainerSpec) int64 {
	current := q.containerCount.Load()
	used := q.vfsQuotaUsed.Load()
	estimate := int64(0)
	if current > 0 && used > 0 {
		estimate = used / current
		if used%current != 0 {
			estimate++
		}
	}
	if candidate != nil && candidate.Resources.VFSQuotaBytes > estimate {
		estimate = candidate.Resources.VFSQuotaBytes
	}
	return estimate
}
