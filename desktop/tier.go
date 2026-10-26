package main

import (
	"fmt"
	"time"
)

// "优先 / 备用"两级选择（与安卓版 TierPicker.kt 一致）。
//
// 地区分级后，内核里是：
//   🇸🇬 新加坡·自动（选择组） → 🇸🇬 新加坡·优先（测速组） / 🇸🇬 新加坡·备用（测速组）
// 内核只负责在每一级里挑最快的节点；选哪一级由这里决定：
//   优先级里最快的 比 备用级里最快的 慢 超过 BackupPenaltyMs → 用备用，否则用优先。
//   优先级全部超时 → 备用；备用全部超时 → 优先。

type tierResult struct {
	Chosen     string // 选中的子组名
	PrimaryMin int    // 优先级最快延迟（ms），0 = 全部超时
	BackupMin  int
	Changed    bool
}

func (t tierResult) describe(region *Region, penalty int) string {
	ms := func(v int) string {
		if v <= 0 {
			return "全部超时"
		}
		return fmt.Sprintf("%dms", v)
	}
	why := ""
	switch {
	case t.PrimaryMin <= 0 && t.BackupMin <= 0:
		why = "两级都超时，保持优先"
	case t.PrimaryMin <= 0:
		why = "优先全部超时，改用备用"
	case t.BackupMin <= 0:
		why = "备用全部超时，用优先"
	case t.Chosen == region.BackupGroup():
		why = fmt.Sprintf("优先比备用慢 %dms，超过 %dms，改用备用", t.PrimaryMin-t.BackupMin, penalty)
	default:
		why = fmt.Sprintf("优先没有比备用慢 %dms 以上，用优先", penalty)
	}
	return region.Flag + " " + region.DisplayName() + "：优先 " + ms(t.PrimaryMin) + " / 备用 " + ms(t.BackupMin) + " → " + why
}

func minDelay(m map[string]int) int {
	best := 0
	for _, d := range m {
		if d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	return best
}

// tierPick 把两级的节点都测一遍，然后把地区自动组指到该用的那一级
func (a *App) tierPick(api *CoreAPI, region *Region) tierResult {
	penalty := a.store.Settings().BackupPenaltyMs
	p := api.GroupDelay(region.PrimaryGroup())
	b := api.GroupDelay(region.BackupGroup())
	pMin, bMin := minDelay(p), minDelay(b)
	chosen := region.PrimaryGroup()
	switch {
	case pMin <= 0 && bMin <= 0:
	case pMin <= 0:
		chosen = region.BackupGroup()
	case bMin <= 0:
	case pMin > bMin+penalty:
		chosen = region.BackupGroup()
	}
	before := ""
	if px, err := api.Proxies(); err == nil {
		before = px[region.GroupName()].Now
	}
	if before != chosen {
		_ = api.Select(region.GroupName(), chosen)
	}
	return tierResult{Chosen: chosen, PrimaryMin: pMin, BackupMin: bMin, Changed: before != chosen}
}

// isTiered 某地区是否分了优先/备用两级
func (a *App) isTiered(code string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tiered[code]
}

// lockedTieredRegion 当前锁定且分了级的地区；没有返回 nil
func (a *App) lockedTieredRegion() *Region {
	st := a.store.Settings()
	if st.LockedRegion == "" || st.Mode == "direct" {
		return nil
	}
	_, regions := a.runtime()
	for _, r := range regions {
		if r.Code == st.LockedRegion && a.isTiered(r.Code) {
			return r
		}
	}
	return nil
}

// tierLoop 每个测速周期判一次该用优先还是备用（只在锁定了分级地区时才真正测）
func (a *App) tierLoop(api *CoreAPI, sleep func(time.Duration) bool) {
	if !sleep(10 * time.Second) {
		return
	}
	for {
		if r := a.lockedTieredRegion(); r != nil && a.API() == api {
			res := a.tierPick(api, r)
			appendLog("[APP] 分级选择 " + res.describe(r, a.store.Settings().BackupPenaltyMs))
			if res.Changed {
				a.changed()
			}
		}
		if !sleep(time.Duration(testIntervalSec(a.store.Settings())) * time.Second) {
			return
		}
	}
}
