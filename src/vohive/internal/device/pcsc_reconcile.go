package device

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/iniwex5/vohive/internal/backend"
	"github.com/iniwex5/vohive/internal/config"
	"github.com/iniwex5/vohive/pkg/logger"
)

const pcscReconcileInterval = 10 * time.Second
const pcscOfflineFailureThreshold = 3

func (p *Pool) pcscReaderReconcileLoop() {
	if p == nil {
		return
	}
	_ = p.ReconcilePCSCReaders(p.Context())
	ticker := time.NewTicker(pcscReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.Context().Done():
			return
		case <-ticker.C:
			if err := p.ReconcilePCSCReaders(p.Context()); err != nil {
				logger.Debug("PC/SC 热插拔对账失败", "err", err)
			}
		}
	}
}

// ReconcilePCSCReaders synchronizes physical reader/card state with persistent
// lines. It never deletes a line when a card is removed; only the runtime worker
// is stopped so notes, policies and history remain attached to the ICCID.
func (p *Pool) ReconcilePCSCReaders(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.pcscReconcileMu.Lock()
	defer p.pcscReconcileMu.Unlock()

	readers, err := p.DiscoverPCSCReaders(ctx)
	if err != nil {
		return err
	}
	liveByReader := make(map[string]PCSCReaderDevice, len(readers))
	for _, reader := range readers {
		liveByReader[strings.TrimSpace(reader.ReaderName)] = reader
	}

	// Evict stale reader workers first. This also handles swapping a different
	// card into the same slot without ever rebinding the old ICCID line.
	for _, worker := range p.GetAllWorkers() {
		if worker == nil || !strings.EqualFold(workerBackendMode(worker), backend.BackendPCSC) {
			continue
		}
		reader, exists := liveByReader[strings.TrimSpace(worker.Config.ReaderName)]
		if exists && reader.Error == "" && strings.TrimSpace(reader.ICCID) == strings.TrimSpace(worker.Config.CardICCID) {
			delete(p.pcscFailures, worker.ID)
			continue
		}
		if !exists || reader.Error != "" {
			p.pcscFailures[worker.ID]++
			if p.pcscFailures[worker.ID] < pcscOfflineFailureThreshold {
				logger.Debug("PC/SC 卡片探测暂时失败，保留运行时等待确认",
					"device", worker.ID,
					"reader", worker.Config.ReaderName,
					"failures", p.pcscFailures[worker.ID],
					"threshold", pcscOfflineFailureThreshold)
				continue
			}
		}
		delete(p.pcscFailures, worker.ID)
		if err := p.RemoveWorker(worker.ID); err != nil {
			logger.Debug("停止离线 PC/SC 线路失败", "device", worker.ID, "err", err)
		} else {
			logger.Info("PC/SC 卡片离线，已停止运行时并保留线路", "device", worker.ID, "reader", worker.Config.ReaderName, "iccid", worker.Config.CardICCID)
		}
	}

	for _, reader := range readers {
		if reader.Error != "" || strings.TrimSpace(reader.ICCID) == "" {
			continue
		}
		devices := config.ListDevices()
		var matched *config.DeviceConfig
		for i := range devices {
			if strings.EqualFold(strings.TrimSpace(devices[i].CardICCID), strings.TrimSpace(reader.ICCID)) {
				copy := devices[i]
				matched = &copy
				break
			}
		}

		if matched == nil {
			if FreeDeviceLimitReached(len(devices)) {
				logger.Warn("发现新的 PC/SC 卡片，但设备额度已满", "reader", reader.ReaderName, "iccid", reader.ICCID, "limit", DefaultFreeDeviceLimit)
				continue
			}
			cfg := config.DeviceConfig{
				ID:                nextPCSCDeviceID(reader.ICCID, devices),
				Name:              pcscLineName(reader.ICCID),
				DeviceBackend:     backend.BackendPCSC,
				ESIMTransport:     config.ESIMTransportPCSC,
				ReaderName:        strings.TrimSpace(reader.ReaderName),
				CardICCID:         strings.TrimSpace(reader.ICCID),
				ProvisioningState: "draft",
				SMSEnabled:        true,
			}
			if err := config.AddDeviceInFile(config.GetConfigPath(), cfg); err != nil {
				logger.Warn("自动创建 PC/SC 草稿线路失败", "reader", reader.ReaderName, "iccid", reader.ICCID, "err", err)
				continue
			}
			matched = &cfg
			logger.Info("已按 ICCID 自动创建 PC/SC 草稿线路", "device", cfg.ID, "reader", reader.ReaderName, "iccid", reader.ICCID)
		} else {
			updated := *matched
			updated.ReaderName = strings.TrimSpace(reader.ReaderName)
			updated.CardICCID = strings.TrimSpace(reader.ICCID)
			updated.DeviceBackend = backend.BackendPCSC
			updated.ESIMTransport = config.ESIMTransportPCSC
			updated.ProvisioningState = pcscProvisioningState(updated.ModemIMEI)
			if updated.ReaderName != matched.ReaderName || updated.DeviceBackend != matched.DeviceBackend ||
				updated.ESIMTransport != matched.ESIMTransport || updated.ProvisioningState != matched.ProvisioningState {
				if err := config.UpdateDeviceInFile(config.GetConfigPath(), matched.ID, updated); err != nil {
					logger.Warn("更新 PC/SC 线路读卡器绑定失败", "device", matched.ID, "err", err)
					continue
				}
			}
			matched = &updated
		}

		if p.GetWorker(matched.ID) == nil && !p.isWorkerRebuilding(matched.ID) {
			if _, err := p.AddWorkerFromConfig(*matched); err != nil {
				logger.Warn("启动 PC/SC 线路失败", "device", matched.ID, "reader", reader.ReaderName, "err", err)
			}
		}
	}
	return nil
}

func pcscLineName(iccid string) string {
	iccid = strings.TrimSpace(iccid)
	if len(iccid) > 4 {
		iccid = iccid[len(iccid)-4:]
	}
	return "eUICC · " + iccid
}

func nextPCSCDeviceID(iccid string, devices []config.DeviceConfig) string {
	var digits strings.Builder
	for _, r := range iccid {
		if unicode.IsDigit(r) {
			digits.WriteRune(r)
		}
	}
	suffix := digits.String()
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	if suffix == "" {
		suffix = "reader"
	}
	base := "pcsc_" + suffix
	used := make(map[string]struct{}, len(devices))
	for _, dev := range devices {
		used[strings.TrimSpace(dev.ID)] = struct{}{}
	}
	if _, exists := used[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}
