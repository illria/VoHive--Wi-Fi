package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/damonto/euicc-go/driver"
	euiccccid "github.com/damonto/euicc-go/driver/ccid"
	"github.com/damonto/euicc-go/lpa"
	"github.com/iniwex5/vohive/internal/apduarbiter"
	"github.com/iniwex5/vohive/internal/backend"
	"github.com/iniwex5/vohive/internal/config"
	"github.com/iniwex5/vohive/internal/esim"
	"github.com/iniwex5/vohive/pkg/logger"
	"github.com/iniwex5/vohive/pkg/smscodec"
)

type leasedPCSCCardChannel struct {
	driver.SmartCardChannel
	lease *apduarbiter.Lease
}

func (c *leasedPCSCCardChannel) Disconnect() error {
	if c == nil {
		return nil
	}
	err := c.SmartCardChannel.Disconnect()
	if c.lease != nil {
		c.lease.Release()
		c.lease = nil
	}
	return err
}

func pcscProvisioningState(imei string) string {
	if !config.IsValidIMEI(imei) {
		return "draft"
	}
	return "ready"
}

func newPCSCEsimManager(
	w *Worker,
	onBefore func(esim.SwitchOperation, string) uint64,
	onAfter func(uint64),
	onFailed func(uint64, error),
	onDegraded func(uint64, esim.SwitchPhase, error),
	onPhase func(uint64, esim.SwitchPhase),
) *esim.Manager {
	callbacks := esim.ChannelFactorySwitchCallbacks{}
	if onBefore != nil {
		callbacks.OnBeforeSwitch = onBefore
	}
	if onAfter != nil {
		callbacks.OnAfterSwitch = func(_ esim.SwitchOperation, token uint64) { onAfter(token) }
	}
	if onFailed != nil {
		callbacks.OnSwitchFailed = func(_ esim.SwitchOperation, token uint64, err error) { onFailed(token, err) }
	}
	if onDegraded != nil {
		callbacks.OnSwitchDegraded = func(_ esim.SwitchOperation, token uint64, phase esim.SwitchPhase, err error) {
			onDegraded(token, phase, err)
		}
	}
	if onPhase != nil {
		callbacks.OnSwitchPhase = func(_ esim.SwitchOperation, token uint64, phase esim.SwitchPhase) { onPhase(token, phase) }
	}

	readerName := strings.TrimSpace(w.Config.ReaderName)
	if w.APDUArbiter == nil {
		w.APDUArbiter = apduarbiter.New(w.ID, apduarbiter.Options{MaxLeaseHold: 10 * time.Minute, MaxSessions: 3, MaxQMITransports: 3})
	}
	mgr := esim.NewManagerWithChannelFactoryCallbacks(w.ID, func(aid []byte) (*lpa.Client, error) {
		leaseCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		lease, err := w.APDUArbiter.AcquireTransport(leaseCtx, apduarbiter.Request{
			Owner: "esim_pcsc",
			Mode:  "PCSC",
			Class: apduarbiter.APDUClassEUICCRead,
			Scope: apduarbiter.TransportScopeExclusive,
		})
		if err != nil {
			return nil, err
		}
		channel, err := euiccccid.NewWithReader(readerName)
		if err != nil {
			lease.Release()
			return nil, err
		}
		leased := &leasedPCSCCardChannel{SmartCardChannel: channel, lease: lease}
		client, err := lpa.New(&lpa.Options{Channel: leased, AID: aid, MSS: 120})
		if err != nil {
			_ = leased.Disconnect()
			return nil, err
		}
		return client, nil
	}, nil, callbacks)
	mgr.SetAPDUArbiter(w.APDUArbiter)
	return mgr
}

func (p *Pool) addPCSCWorkerFromConfig(devCfg config.DeviceConfig, attempt uint64) (*Worker, error) {
	ctx, cancel := context.WithTimeout(p.Context(), pcscProbeTimeout)
	defer cancel()
	be, err := backend.NewPCSCBackend(ctx, devCfg.ReaderName, devCfg.ModemIMEI)
	if err != nil {
		return nil, err
	}
	liveICCID, err := be.GetICCID(ctx)
	if err != nil {
		_ = be.Close()
		return nil, err
	}
	identity := backend.PCSCIdentity{
		ReaderName: strings.TrimSpace(devCfg.ReaderName),
		ICCID:      strings.TrimSpace(liveICCID),
	}
	configuredICCID := strings.TrimSpace(devCfg.CardICCID)
	if configuredICCID != "" && configuredICCID != strings.TrimSpace(identity.ICCID) {
		_ = be.Close()
		return nil, fmt.Errorf("PC/SC 线路 %s 绑定 ICCID %s，但读卡器当前卡片为 %s", devCfg.ID, configuredICCID, identity.ICCID)
	}
	devCfg.DeviceBackend = backend.BackendPCSC
	devCfg.ESIMTransport = config.ESIMTransportPCSC
	devCfg.ReaderName = strings.TrimSpace(identity.ReaderName)
	devCfg.CardICCID = strings.TrimSpace(identity.ICCID)
	devCfg.ProvisioningState = pcscProvisioningState(devCfg.ModemIMEI)

	w := &Worker{
		ID:          devCfg.ID,
		Config:      devCfg,
		Backend:     be,
		APDUArbiter: apduarbiter.New(devCfg.ID, apduarbiter.Options{MaxLeaseHold: 10 * time.Minute, MaxSessions: 3, MaxQMITransports: 3}),
		Pool:        p,
		stop:        make(chan struct{}),
		reassembler: smscodec.NewReassembler(),
		smsMode:     smsModePCSC,
	}
	be.SetAPDUArbiter(w.APDUArbiter)
	p.assignWorkerGeneration(w)
	onBefore, onAfter, onFailed, onDegraded, onPhase := p.newESIMSwitchCallbacks(devCfg.ID)
	w.EsimMgr = newPCSCEsimManager(w, onBefore, onAfter, onFailed, onDegraded, onPhase)

	if !p.isRebuildAttemptCurrent(devCfg.ID, attempt) {
		_ = be.Close()
		return nil, fmt.Errorf("设备 %s 启动流程已超时放弃", devCfg.ID)
	}
	p.mu.Lock()
	if existing := p.workers[devCfg.ID]; existing != nil {
		p.mu.Unlock()
		_ = be.Close()
		return nil, fmt.Errorf("设备已存在")
	}
	p.workers[devCfg.ID] = w
	p.mu.Unlock()
	w.uimIndicationsReady.Store(true)

	go func() {
		w.PreWarmCache()
		p.resolveAndApplyPolicy(w, "pcsc_startup")
		p.broadcastVoWiFiStateChange(w.ID)
	}()
	logger.Info("PC/SC 线路已启动", "device", devCfg.ID, "reader", devCfg.ReaderName, "iccid", devCfg.CardICCID, "provisioning_state", devCfg.ProvisioningState)
	return w, nil
}
