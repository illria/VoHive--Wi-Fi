package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/upstreamproxy"
	"gorm.io/gorm"
)

// UpstreamProxy 前置代理实例（用于代理 VoWiFi 的 ePDG 连接）
// 通过 Socks5 UDP Associate 将 IKE/ESP 流量转发到 ePDG
type UpstreamProxy struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	Addr      string    `json:"addr"`               // Socks5 服务器地址 (host:port)
	Username  string    `json:"username"`           // 可选鉴权用户名
	Password  string    `json:"password,omitempty"` // 可选鉴权密码
	Enabled   bool      `json:"enabled"`            // 是否启用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UpstreamProxyCountryRule 将 SIM home country 路由到指定前置代理。
type UpstreamProxyCountryRule struct {
	CountryCode     string    `gorm:"primaryKey" json:"country_code"`
	UpstreamProxyID string    `gorm:"index" json:"upstream_proxy_id"`
	PinnedProxyID   string    `gorm:"index" json:"pinned_proxy_id"`
	Enabled         bool      `json:"enabled"`
	Required        bool      `json:"required"`
	AutoFailover    bool      `json:"auto_failover"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (UpstreamProxyCountryRule) TableName() string {
	return "upstream_proxy_country_rules"
}

// UpstreamProxyCountryMember stores an ordered proxy pool for one country.
// The legacy UpstreamProxyID column remains populated for backward compatibility.
type UpstreamProxyCountryMember struct {
	CountryCode     string    `gorm:"primaryKey" json:"country_code"`
	UpstreamProxyID string    `gorm:"primaryKey;index" json:"upstream_proxy_id"`
	Priority        int       `json:"priority"`
	Enabled         bool      `json:"enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (UpstreamProxyCountryMember) TableName() string {
	return "upstream_proxy_country_members"
}

type UpstreamProxyHealth struct {
	UpstreamProxyID    string    `gorm:"primaryKey" json:"upstream_proxy_id"`
	Healthy            bool      `json:"healthy"`
	Stage              string    `json:"stage"`
	LastError          string    `json:"last_error,omitempty"`
	ConsecutiveFailure int       `json:"consecutive_failures"`
	CheckedAt          time.Time `json:"checked_at"`
	CooldownUntil      time.Time `json:"cooldown_until,omitempty"`
}

func (UpstreamProxyHealth) TableName() string { return "upstream_proxy_health" }

// ── UpstreamProxy CRUD ──

// ListUpstreamProxies 列出所有前置代理实例
func ListUpstreamProxies() ([]UpstreamProxy, error) {
	var out []UpstreamProxy
	if err := DB.Order("id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetUpstreamProxyByID 根据 ID 获取前置代理
func GetUpstreamProxyByID(id string) (*UpstreamProxy, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("empty id")
	}
	var out UpstreamProxy
	err := DB.First(&out, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// UpsertUpstreamProxy 创建或更新前置代理
func UpsertUpstreamProxy(p UpstreamProxy) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("empty id")
	}
	if strings.TrimSpace(p.Addr) == "" {
		return errors.New("empty addr")
	}
	return DB.Save(&p).Error
}

// DeleteUpstreamProxy 删除前置代理（同时清理关联的国家规则）
func DeleteUpstreamProxy(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("empty id")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&UpstreamProxyCountryMember{}, "upstream_proxy_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&UpstreamProxyHealth{}, "upstream_proxy_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Model(&UpstreamProxyCountryRule{}).Where("upstream_proxy_id = ?", id).Update("upstream_proxy_id", "").Error; err != nil {
			return err
		}
		if err := tx.Model(&UpstreamProxyCountryRule{}).Where("pinned_proxy_id = ?", id).Update("pinned_proxy_id", "").Error; err != nil {
			return err
		}
		return tx.Delete(&UpstreamProxy{}, "id = ?", id).Error
	})
}

// ── UpstreamProxyCountryRule 国家规则管理 ──

func ListUpstreamProxyCountryRules() ([]UpstreamProxyCountryRule, error) {
	var out []UpstreamProxyCountryRule
	if err := DB.Order("country_code asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func UpsertUpstreamProxyCountryRule(rule UpstreamProxyCountryRule) error {
	rule.CountryCode = upstreamproxy.NormalizeCountryCode(rule.CountryCode)
	rule.UpstreamProxyID = strings.TrimSpace(rule.UpstreamProxyID)
	rule.PinnedProxyID = strings.TrimSpace(rule.PinnedProxyID)
	if rule.CountryCode == "" {
		return errors.New("empty country_code")
	}
	if rule.UpstreamProxyID == "" {
		return errors.New("empty upstream_proxy_id")
	}
	rule.UpdatedAt = time.Now()
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&rule).Error; err != nil {
			return err
		}
		member := UpstreamProxyCountryMember{
			CountryCode:     rule.CountryCode,
			UpstreamProxyID: rule.UpstreamProxyID,
			Priority:        0,
			Enabled:         true,
			UpdatedAt:       time.Now(),
		}
		return tx.Save(&member).Error
	})
}

func UpsertUpstreamProxyCountryRuleSet(rule UpstreamProxyCountryRule, proxyIDs []string) error {
	rule.CountryCode = upstreamproxy.NormalizeCountryCode(rule.CountryCode)
	if rule.CountryCode == "" {
		return errors.New("empty country_code")
	}
	clean := make([]string, 0, len(proxyIDs))
	seen := map[string]struct{}{}
	for _, id := range proxyIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return errors.New("empty upstream_proxy_ids")
	}
	rule.UpstreamProxyID = clean[0]
	rule.PinnedProxyID = strings.TrimSpace(rule.PinnedProxyID)
	if rule.PinnedProxyID != "" {
		if _, ok := seen[rule.PinnedProxyID]; !ok {
			return errors.New("pinned_proxy_id is not a country member")
		}
	}
	rule.UpdatedAt = time.Now()
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&rule).Error; err != nil {
			return err
		}
		if err := tx.Delete(&UpstreamProxyCountryMember{}, "country_code = ?", rule.CountryCode).Error; err != nil {
			return err
		}
		for priority, id := range clean {
			member := UpstreamProxyCountryMember{CountryCode: rule.CountryCode, UpstreamProxyID: id, Priority: priority, Enabled: true, UpdatedAt: time.Now()}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func ListUpstreamProxyCountryMembers(countryCode string) ([]UpstreamProxyCountryMember, error) {
	countryCode = upstreamproxy.NormalizeCountryCode(countryCode)
	var out []UpstreamProxyCountryMember
	if err := DB.Where("country_code = ?", countryCode).Order("priority asc, upstream_proxy_id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func ListUpstreamProxyHealth() ([]UpstreamProxyHealth, error) {
	var out []UpstreamProxyHealth
	if err := DB.Order("upstream_proxy_id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func DeleteUpstreamProxyCountryRule(countryCode string) error {
	countryCode = upstreamproxy.NormalizeCountryCode(countryCode)
	if countryCode == "" {
		return errors.New("empty country_code")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&UpstreamProxyCountryMember{}, "country_code = ?", countryCode).Error; err != nil {
			return err
		}
		return tx.Delete(&UpstreamProxyCountryRule{}, "country_code = ?", countryCode).Error
	})
}

func GetCountryUpstreamProxy(countryCode string) (*UpstreamProxy, error) {
	countryCode = upstreamproxy.NormalizeCountryCode(countryCode)
	if countryCode == "" {
		return nil, nil
	}
	var rule UpstreamProxyCountryRule
	err := DB.First(&rule, "country_code = ?", countryCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !rule.Enabled || strings.TrimSpace(rule.UpstreamProxyID) == "" {
		return nil, nil
	}
	proxy, err := GetUpstreamProxyByID(rule.UpstreamProxyID)
	if err != nil || proxy == nil || !proxy.Enabled {
		return nil, err
	}
	return proxy, nil
}

func GetHomeMCCUpstreamProxy(homeMCC string) (*UpstreamProxy, string, error) {
	countryCode, ok := upstreamproxy.CountryCodeFromHomeMCC(homeMCC)
	if !ok {
		return nil, "", nil
	}
	proxy, err := GetCountryUpstreamProxy(countryCode)
	return proxy, countryCode, err
}

type UpstreamProxySelection struct {
	Proxy       *UpstreamProxy
	CountryCode string
	Required    bool
	Attempted   []string
}

// SelectHomeMCCUpstreamProxy probes an ordered country pool and pins the first
// healthy proxy for the caller's upcoming session. It never changes an already
// running session; failover is evaluated only during the next start.
func SelectHomeMCCUpstreamProxy(ctx context.Context, homeMCC string) (UpstreamProxySelection, error) {
	countryCode, ok := upstreamproxy.CountryCodeFromHomeMCC(homeMCC)
	if !ok {
		return UpstreamProxySelection{}, nil
	}
	selection := UpstreamProxySelection{CountryCode: countryCode}
	var rule UpstreamProxyCountryRule
	if err := DB.First(&rule, "country_code = ?", countryCode).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return selection, nil
		}
		return selection, err
	}
	selection.Required = rule.Required
	if !rule.Enabled {
		return selection, nil
	}
	members, err := ListUpstreamProxyCountryMembers(countryCode)
	if err != nil {
		return selection, err
	}
	if len(members) == 0 && strings.TrimSpace(rule.UpstreamProxyID) != "" {
		members = []UpstreamProxyCountryMember{{CountryCode: countryCode, UpstreamProxyID: rule.UpstreamProxyID, Enabled: true}}
	}
	pinned := strings.TrimSpace(rule.PinnedProxyID)
	if pinned == "" {
		pinned = strings.TrimSpace(rule.UpstreamProxyID)
	}
	healthRows, _ := ListUpstreamProxyHealth()
	healthByID := make(map[string]UpstreamProxyHealth, len(healthRows))
	for _, health := range healthRows {
		healthByID[health.UpstreamProxyID] = health
	}
	now := time.Now()
	sort.SliceStable(members, func(i, j int) bool {
		if rule.AutoFailover {
			iCooling := healthByID[members[i].UpstreamProxyID].CooldownUntil.After(now)
			jCooling := healthByID[members[j].UpstreamProxyID].CooldownUntil.After(now)
			if iCooling != jCooling {
				return !iCooling
			}
		}
		iPinned := members[i].UpstreamProxyID == pinned
		jPinned := members[j].UpstreamProxyID == pinned
		if iPinned != jPinned {
			return iPinned
		}
		return members[i].Priority < members[j].Priority
	})

	for _, member := range members {
		if !member.Enabled {
			continue
		}
		proxy, getErr := GetUpstreamProxyByID(member.UpstreamProxyID)
		if getErr != nil {
			return selection, getErr
		}
		if proxy == nil || !proxy.Enabled {
			selection.Attempted = append(selection.Attempted, member.UpstreamProxyID+":disabled")
			if !rule.AutoFailover {
				break
			}
			continue
		}
		result, probeErr := upstreamproxy.ProbeSOCKS5(ctx, upstreamproxy.ProbeConfig{
			ProxyAddr: proxy.Addr,
			Username:  proxy.Username,
			Password:  proxy.Password,
			Timeout:   5 * time.Second,
		})
		RecordUpstreamProxyProbe(proxy.ID, result, probeErr)
		if probeErr == nil && result.OK() {
			selection.Proxy = proxy
			selection.Attempted = append(selection.Attempted, proxy.ID+":ok")
			return selection, nil
		}
		selection.Attempted = append(selection.Attempted, proxy.ID+":"+result.Stage)
		if !rule.AutoFailover {
			break
		}
	}
	if selection.Required {
		return selection, fmt.Errorf("国家 %s 的必需代理池无可用出口（%s）", countryCode, strings.Join(selection.Attempted, ", "))
	}
	return selection, nil
}

// RecordUpstreamProxyProbe persists both pool-selection and final preflight
// results so a failed endpoint is placed behind healthy members on the next
// session without changing the route of a session already in progress.
func RecordUpstreamProxyProbe(proxyID string, result upstreamproxy.ProbeResult, probeErr error) {
	if DB == nil || strings.TrimSpace(proxyID) == "" {
		return
	}
	now := time.Now()
	var previous UpstreamProxyHealth
	_ = DB.First(&previous, "upstream_proxy_id = ?", proxyID).Error
	health := UpstreamProxyHealth{
		UpstreamProxyID: proxyID,
		Healthy:         probeErr == nil && result.OK(),
		Stage:           result.Stage,
		CheckedAt:       now,
	}
	if health.Healthy {
		health.ConsecutiveFailure = 0
	} else {
		health.ConsecutiveFailure = previous.ConsecutiveFailure + 1
		health.LastError = result.FailureSummary()
		if strings.TrimSpace(health.LastError) == "" && probeErr != nil {
			health.LastError = probeErr.Error()
		}
		cooldown := time.Duration(min(health.ConsecutiveFailure, 6)) * 15 * time.Second
		health.CooldownUntil = now.Add(cooldown)
	}
	_ = DB.Save(&health).Error
}
