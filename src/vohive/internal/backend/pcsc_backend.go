package backend

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	uiccccid "github.com/damonto/uicc-go/ccid"
	"github.com/damonto/uicc-go/usim"
	"github.com/iniwex5/vohive/internal/apduarbiter"
)

const (
	pcscIdentityCacheTTL = 3 * time.Second
	pcscMaxChannel       = 19
)

var errPCSCUnsupported = errors.New("PC/SC 读卡器不支持该蜂窝模组操作")

// PCSCIdentity 是从普通 PC/SC 读卡器中的 UICC/eUICC 实时读取的身份快照。
// ReaderName + ICCID 用于热插拔对账；IMEI 不属于卡片，需要用户为线路单独填写。
type PCSCIdentity struct {
	ReaderName      string
	ICCID           string
	IMSI            string
	MCC             string
	MNC             string
	GID1            string
	SMSC            string
	PrivateIdentity string
	PublicIdentity  string
	HomeDomain      string
	USIMAID         string
	ISIMAID         string
}

// PCSCBackend 让普通读卡器参与现有 DeviceBackend/VoWiFi 管线。
// 所有卡片 APDU 都走现有纯 Go PC/SC 驱动，不引入新的程序依赖。
type PCSCBackend struct {
	readerName string
	imei       string

	mu            sync.Mutex
	identity      PCSCIdentity
	identityAt    time.Time
	logicalReader *uiccccid.Reader
	logicalID     int
	logicalLease  *apduarbiter.Lease
	apduArbiter   *apduarbiter.Arbiter
	mode          OperatingMode
	closed        bool
}

func NewPCSCBackend(ctx context.Context, readerName, imei string) (*PCSCBackend, error) {
	readerName = strings.TrimSpace(readerName)
	if readerName == "" {
		return nil, errors.New("PC/SC reader_name 不能为空")
	}
	b := &PCSCBackend{readerName: readerName, imei: strings.TrimSpace(imei), mode: ModeRFOff}
	if _, err := b.refreshIdentity(ctx, true); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *PCSCBackend) Mode() string { return BackendPCSC }

func (b *PCSCBackend) SetAPDUArbiter(arbiter *apduarbiter.Arbiter) {
	b.mu.Lock()
	b.apduArbiter = arbiter
	b.mu.Unlock()
}

func (b *PCSCBackend) Identity(ctx context.Context, force bool) (PCSCIdentity, error) {
	return b.refreshIdentity(ctx, force)
}

func (b *PCSCBackend) CachedIdentity() (PCSCIdentity, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.identity, strings.TrimSpace(b.identity.ICCID) != ""
}

func (b *PCSCBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.logicalReader == nil {
		if b.logicalLease != nil {
			b.logicalLease.Release()
			b.logicalLease = nil
		}
		return nil
	}
	err := b.logicalReader.Close()
	b.logicalReader = nil
	b.logicalID = 0
	if b.logicalLease != nil {
		b.logicalLease.Release()
		b.logicalLease = nil
	}
	return err
}

func ProbePCSCIdentity(ctx context.Context, readerName string) (PCSCIdentity, error) {
	b := &PCSCBackend{readerName: strings.TrimSpace(readerName), mode: ModeRFOff}
	return b.refreshIdentity(ctx, true)
}

func (b *PCSCBackend) refreshIdentity(ctx context.Context, force bool) (PCSCIdentity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return PCSCIdentity{}, errors.New("PC/SC backend 已关闭")
	}
	if !force && b.identity.ICCID != "" && time.Since(b.identityAt) < pcscIdentityCacheTTL {
		return b.identity, nil
	}
	// 避免在 AKA 逻辑通道会话中切换基本通道上的应用；优先返回已有快照。
	if b.logicalReader != nil && b.identity.ICCID != "" {
		return b.identity, nil
	}

	raw, err := uiccccid.Open(ctx, b.readerName)
	if err != nil {
		return PCSCIdentity{}, fmt.Errorf("打开 PC/SC 读卡器 %q 失败: %w", b.readerName, err)
	}
	reader, err := usim.NewReader(raw)
	if err != nil {
		_ = raw.Close()
		return PCSCIdentity{}, err
	}
	apps, appsErr := reader.ListApplications(ctx)
	card, err := usim.New(ctx, reader)
	if err != nil {
		_ = reader.Close()
		return PCSCIdentity{}, fmt.Errorf("读取 %q 中的 UICC 身份失败: %w", b.readerName, err)
	}
	defer card.Close()

	out := PCSCIdentity{
		ReaderName:      b.readerName,
		ICCID:           strings.TrimSpace(card.ICCID()),
		IMSI:            strings.TrimSpace(card.IMSI()),
		MCC:             strings.TrimSpace(card.MCC()),
		MNC:             strings.TrimSpace(card.MNC()),
		GID1:            strings.TrimSpace(card.GID1()),
		SMSC:            strings.TrimSpace(card.SMSC()),
		PrivateIdentity: strings.TrimSpace(card.PrivateIdentity()),
		PublicIdentity:  strings.TrimSpace(card.PublicIdentity()),
		HomeDomain:      strings.TrimSpace(card.HomeDomain()),
	}
	if appsErr == nil {
		for _, app := range apps {
			aidHex := strings.ToUpper(hex.EncodeToString(app.AID))
			switch {
			case strings.HasPrefix(aidHex, "A0000000871002") && out.USIMAID == "":
				out.USIMAID = aidHex
			case strings.HasPrefix(aidHex, "A0000000871004") && out.ISIMAID == "":
				out.ISIMAID = aidHex
			}
		}
	}
	if out.ICCID == "" {
		return PCSCIdentity{}, fmt.Errorf("读卡器 %q 中未读取到 ICCID", b.readerName)
	}
	b.identity = out
	b.identityAt = time.Now()
	return out, nil
}

func (b *PCSCBackend) GetIMEI(context.Context) (string, error) {
	if strings.TrimSpace(b.imei) == "" {
		return "", errors.New("PC/SC 线路尚未填写 IMEI")
	}
	return b.imei, nil
}

func (b *PCSCBackend) GetIMSI(ctx context.Context) (string, error) {
	id, err := b.refreshIdentity(ctx, false)
	return id.IMSI, err
}

func (b *PCSCBackend) GetIMSILive(ctx context.Context) (string, error) {
	id, err := b.refreshIdentity(ctx, true)
	return id.IMSI, err
}

func (b *PCSCBackend) GetICCID(ctx context.Context) (string, error) {
	id, err := b.refreshIdentity(ctx, false)
	return id.ICCID, err
}

func (b *PCSCBackend) GetICCIDLive(ctx context.Context) (string, error) {
	id, err := b.refreshIdentity(ctx, true)
	return id.ICCID, err
}

func (b *PCSCBackend) GetMSISDN(context.Context) (string, error) { return "", nil }

func (b *PCSCBackend) GetRevision(context.Context) (string, error) {
	return "PC/SC · " + b.readerName, nil
}

func (b *PCSCBackend) GetSignalInfo(context.Context) (*SignalInfo, error) {
	return &SignalInfo{}, nil
}

func (b *PCSCBackend) GetServingSystem(context.Context) (*ServingSystem, error) {
	return &ServingSystem{RegStatusText: "reader_only", NetworkMode: "PC/SC"}, nil
}

func (b *PCSCBackend) IsSimInserted(ctx context.Context) (bool, error) {
	_, err := b.refreshIdentity(ctx, true)
	return err == nil, err
}

func (b *PCSCBackend) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	id, err := b.refreshIdentity(ctx, false)
	return id.MCC, id.MNC, err
}

func (b *PCSCBackend) GetNativeSPN(context.Context) (string, error)     { return "", nil }
func (b *PCSCBackend) GetNativeSPNLive(context.Context) (string, error) { return "", nil }

func (b *PCSCBackend) GetSIMMetadata(ctx context.Context) (*SIMMetadata, error) {
	id, err := b.refreshIdentity(ctx, false)
	if err != nil {
		return nil, err
	}
	return &SIMMetadata{NativeMCC: id.MCC, NativeMNC: id.MNC, GID1: id.GID1}, nil
}

func (b *PCSCBackend) GetSIMMetadataLive(ctx context.Context) (*SIMMetadata, error) {
	if _, err := b.refreshIdentity(ctx, true); err != nil {
		return nil, err
	}
	return b.GetSIMMetadata(ctx)
}

func (b *PCSCBackend) GetSMSC(ctx context.Context) (string, error) {
	id, err := b.refreshIdentity(ctx, false)
	return id.SMSC, err
}

func (b *PCSCBackend) GetISIMCredentials(ctx context.Context) (impi, domain string, impu []string, err error) {
	id, err := b.refreshIdentity(ctx, false)
	if err != nil {
		return "", "", nil, err
	}
	if id.PublicIdentity != "" {
		impu = []string{id.PublicIdentity}
	}
	return id.PrivateIdentity, id.HomeDomain, impu, nil
}

func (b *PCSCBackend) SendSMS(context.Context, string, string) error { return errPCSCUnsupported }
func (b *PCSCBackend) ReadSMS(context.Context, int) (*SMS, error)    { return nil, errPCSCUnsupported }
func (b *PCSCBackend) DeleteSMS(context.Context, int) error          { return errPCSCUnsupported }
func (b *PCSCBackend) ListSMS(context.Context) ([]SMSSummary, error) { return nil, errPCSCUnsupported }
func (b *PCSCBackend) DeleteAllSMS(context.Context) error            { return errPCSCUnsupported }

func (b *PCSCBackend) SetOperatingMode(_ context.Context, mode OperatingMode) error {
	b.mu.Lock()
	b.mode = mode
	b.mu.Unlock()
	return nil
}

func (b *PCSCBackend) GetOperatingMode(context.Context) (OperatingMode, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mode, nil
}

func (b *PCSCBackend) Reboot(context.Context) error { return errPCSCUnsupported }

func (b *PCSCBackend) ResolveSIMAuthAID(ctx context.Context, app, fallbackAID string) (string, string, error) {
	id, err := b.refreshIdentity(ctx, false)
	if err != nil {
		return "", "", err
	}
	if strings.EqualFold(strings.TrimSpace(app), "isim") && id.ISIMAID != "" {
		return id.ISIMAID, "pcsc_ef_dir", nil
	}
	if id.USIMAID != "" {
		return id.USIMAID, "pcsc_ef_dir", nil
	}
	return strings.TrimSpace(fallbackAID), "fallback", nil
}

func (b *PCSCBackend) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	aidBytes, err := hex.DecodeString(strings.TrimSpace(aid))
	if err != nil || len(aidBytes) == 0 {
		return 0, fmt.Errorf("无效 AID %q", aid)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, errors.New("PC/SC backend 已关闭")
	}
	if b.logicalReader != nil {
		return 0, errors.New("PC/SC 逻辑通道已占用")
	}
	var lease *apduarbiter.Lease
	if b.apduArbiter != nil {
		lease, err = b.apduArbiter.AcquireTransport(ctx, apduarbiter.Request{
			Owner: "vowifi_aka",
			Mode:  "PCSC",
			Class: apduarbiter.APDUClassUSIMAKA,
			Scope: apduarbiter.TransportScopeExclusive,
		})
		if err != nil {
			return 0, err
		}
	}
	releaseLease := true
	defer func() {
		if releaseLease && lease != nil {
			lease.Release()
		}
	}()
	reader, err := uiccccid.Open(ctx, b.readerName)
	if err != nil {
		return 0, err
	}
	response, err := reader.Transmit(ctx, []byte{0x00, 0x70, 0x00, 0x00, 0x01})
	if err != nil || len(response) < 3 || !pcscStatusOK(response) {
		_ = reader.Close()
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("打开 PC/SC 逻辑通道失败: %X", response)
	}
	channel := int(response[0])
	if channel <= 0 || channel > pcscMaxChannel {
		_ = reader.Close()
		return 0, fmt.Errorf("PC/SC 返回无效逻辑通道 %d", channel)
	}
	cla, err := pcscClassByteForChannel(0x00, channel)
	if err != nil {
		_ = reader.Close()
		return 0, err
	}
	selectAPDU := append([]byte{cla, 0xA4, 0x04, 0x00, byte(len(aidBytes))}, aidBytes...)
	response, err = reader.Transmit(ctx, selectAPDU)
	if err != nil || len(response) < 2 || (!pcscStatusOK(response) && response[len(response)-2] != 0x61) {
		_, _ = reader.Transmit(ctx, []byte{0x00, 0x70, 0x80, byte(channel), 0x00})
		_ = reader.Close()
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("选择 PC/SC AID 失败: %X", response)
	}
	b.logicalReader = reader
	b.logicalID = channel
	b.logicalLease = lease
	releaseLease = false
	return channel, nil
}

func (b *PCSCBackend) CloseLogicalChannel(ctx context.Context, channelID int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logicalReader == nil {
		return nil
	}
	reader := b.logicalReader
	lease := b.logicalLease
	b.logicalReader = nil
	b.logicalID = 0
	b.logicalLease = nil
	response, txErr := reader.Transmit(ctx, []byte{0x00, 0x70, 0x80, byte(channelID), 0x00})
	closeErr := reader.Close()
	if lease != nil {
		lease.Release()
	}
	if txErr != nil {
		return errors.Join(txErr, closeErr)
	}
	if !pcscStatusOK(response) {
		return errors.Join(fmt.Errorf("关闭 PC/SC 逻辑通道失败: %X", response), closeErr)
	}
	return closeErr
}

func (b *PCSCBackend) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	apdu, err := hex.DecodeString(strings.TrimSpace(command))
	if err != nil || len(apdu) < 4 {
		return "", fmt.Errorf("无效 APDU %q", command)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logicalReader == nil || b.logicalID != channelID {
		return "", fmt.Errorf("PC/SC 逻辑通道 %d 未打开", channelID)
	}
	cla, err := pcscClassByteForChannel(apdu[0], channelID)
	if err != nil {
		return "", err
	}
	apdu[0] = cla
	response, err := b.logicalReader.Transmit(ctx, apdu)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(response)), nil
}

func pcscClassByteForChannel(cla byte, channel int) (byte, error) {
	if channel < 4 {
		return (cla & 0x9C) | byte(channel), nil
	}
	if channel <= pcscMaxChannel {
		return (cla & 0xB0) | 0x40 | byte(channel-4), nil
	}
	return 0, fmt.Errorf("逻辑通道 %d 超过最大值 %d", channel, pcscMaxChannel)
}

func pcscStatusOK(response []byte) bool {
	return len(response) >= 2 && response[len(response)-2] == 0x90 && response[len(response)-1] == 0x00
}
