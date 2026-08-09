package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	vowifimodel "github.com/iniwex5/vowifi-go/internal/vowifi"
	runtimeims "github.com/iniwex5/vowifi-go/internal/vowifi/ims"
	"github.com/iniwex5/vowifi-go/internal/vowifi/runtimecore"
	"github.com/iniwex5/vowifi-go/runtimehost/identity"
	"github.com/iniwex5/vowifi-go/runtimehost/messaging"
)

// imsTunnelEvidence adapts the existing SWu session snapshot to the
// evidence-oriented interface used by the VoCat IMS provider. The IMS layer
// is deliberately not allowed to invent a P-CSCF or local address: both must
// come from the established tunnel (unless a carrier config already supplied
// a P-CSCF that is also proven by that tunnel).
type imsTunnelEvidence struct {
	snapshot runtimecore.TunnelSnapshot
}

func (t imsTunnelEvidence) Evidence() vowifimodel.TunnelEvidence {
	return vowifimodel.TunnelEvidence{
		Established:   t.snapshot.Established,
		Name:          t.snapshot.TUNName,
		DataplaneMode: "userspace",
		LocalIPv4:     ipToString(t.snapshot.IPv4),
		LocalIPv6:     ipToString(t.snapshot.IPv6),
		PCSCF:         append(ipsToStrings(t.snapshot.PCSCFv4), ipsToStrings(t.snapshot.PCSCFv6)...),
		IKEEncryption: t.snapshot.IKEEncr,
		IKEIntegrity:  t.snapshot.IKEInteg,
		IKEDHGroup:    t.snapshot.IKEDH,
	}
}

type runtimeAKAProvider struct {
	sim SIMAdapter
}

func (a runtimeAKAProvider) CheckReady(ctx context.Context, identity vowifimodel.SIMIdentity) (vowifimodel.AKAEvidence, error) {
	if err := ctx.Err(); err != nil {
		return vowifimodel.AKAEvidence{}, err
	}
	if a.sim == nil {
		return vowifimodel.AKAEvidence{}, errors.New("ims: SIM AKA adapter unavailable")
	}
	if strings.TrimSpace(identity.IMSI) == "" {
		return vowifimodel.AKAEvidence{}, errors.New("ims: IMSI unavailable for AKA")
	}
	return vowifimodel.AKAEvidence{Ready: true, Application: "usim"}, nil
}

func (a runtimeAKAProvider) Authenticate(ctx context.Context, _ vowifimodel.SIMIdentity, challenge vowifimodel.AKAChallenge) (vowifimodel.AKAResult, error) {
	if err := ctx.Err(); err != nil {
		return vowifimodel.AKAResult{}, err
	}
	if a.sim == nil {
		return vowifimodel.AKAResult{}, errors.New("ims: SIM AKA adapter unavailable")
	}
	result, err := a.sim.CalculateAKA(challenge.RAND[:], challenge.AUTN[:])
	return vowifimodel.AKAResult{
		RES:                    append([]byte(nil), result.RES...),
		CK:                     append([]byte(nil), result.CK...),
		IK:                     append([]byte(nil), result.IK...),
		AUTS:                   append([]byte(nil), result.AUTS...),
		SynchronizationFailure: len(result.AUTS) > 0,
	}, err
}

type imsRuntimeService struct {
	session  vowifimodel.IMSSession
	sender   vowifimodel.SMSSender
	deviceID string
	imsi     string
	delivery messaging.DeliveryStore
}

func (s *imsRuntimeService) SendSMSWithOptions(ctx context.Context, to, text string, _ messaging.SendOptions) (messaging.SendOutcome, error) {
	if s == nil || s.sender == nil {
		return messaging.SendOutcome{}, errors.New("ims service not ready")
	}
	result, err := s.sender.SendSMS(ctx, vowifimodel.SMSSubmitRequest{Recipient: to, Text: text})
	messageID := fmt.Sprintf("ims-%d", result.SubmittedAt.UnixNano())
	if result.SubmittedAt.IsZero() {
		messageID = fmt.Sprintf("ims-%d", time.Now().UTC().UnixNano())
	}
	if s.delivery != nil {
		state := result.SubmissionStatus
		if err != nil && state == "" {
			state = "failed"
		}
		if state == "" {
			state = "accepted_by_ims"
		}
		_ = s.delivery.CreateSMSDelivery(messageID, s.imsi, s.deviceID, to, text, result.PartsTotal, time.Now().UTC())
		_ = s.delivery.UpdateSMSDeliveryState(messageID, state, errorText(err), result.PartsAccepted, time.Now().UTC())
	}
	if err != nil {
		return messaging.SendOutcome{MessageID: messageID, PartsTotal: result.PartsTotal, DeliveryState: result.SubmissionStatus}, err
	}
	return messaging.SendOutcome{
		MessageID:     messageID,
		PartsTotal:    result.PartsTotal,
		DeliveryState: result.SubmissionStatus,
	}, nil
}

func (s *imsRuntimeService) SendUSSD(context.Context, string) (*messaging.USSDResult, error) {
	return nil, errors.New("USSD over IMS is not implemented")
}

func (s *imsRuntimeService) ContinueUSSD(context.Context, string, string) (*messaging.USSDResult, error) {
	return nil, errors.New("USSD over IMS is not implemented")
}

func (s *imsRuntimeService) CancelUSSD(context.Context, string) error {
	return errors.New("USSD over IMS is not implemented")
}

func startIMSRuntime(
	ctx context.Context,
	inst *Instance,
	deviceID string,
	profile identity.Profile,
	prepared identity.PreparedSession,
	tunnel runtimecore.Session,
	sim SIMAdapter,
	delivery messaging.DeliveryStore,
) (*imsRuntimeService, vowifimodel.IMSSession, vowifimodel.IMSEvidence, bool, error) {
	if tunnel == nil {
		return nil, nil, vowifimodel.IMSEvidence{}, false, errors.New("ims: tunnel session is unavailable")
	}
	if sim == nil {
		return nil, nil, vowifimodel.IMSEvidence{}, false, errors.New("ims: SIM AKA adapter is unavailable")
	}

	profile = identity.NormalizeProfile(profile)
	carrier := prepared.EffectiveCarrier
	imsConfig := runtimeims.Config{
		PCSCF:            strings.TrimSpace(carrier.IMS.PCSCF),
		Transport:        strings.ToLower(strings.TrimSpace(carrier.IMS.Transport)),
		Port:             carrier.IMS.LocalPort,
		PrivateIdentity:  strings.TrimSpace(prepared.IMSIdentity.IMPI),
		PublicIdentity:   strings.TrimSpace(prepared.IMSIdentity.IMPU),
		UserAgent:        strings.TrimSpace(carrier.IMS.UserAgent),
		SMSCenter:        strings.TrimSpace(profile.SMSC),
		SecurityMode:     configuredIMSSecurityMode(),
		TransactionTimeout: 12 * time.Second,
		OnSMS: func(_ context.Context, message runtimeims.ReceivedSMS) error {
			if inst == nil {
				return nil
			}
			inst.mu.RLock()
			notify := inst.smsNotify
			inst.mu.RUnlock()
			if notify != nil {
				notify(message.DeviceID, message.From, message.Text, message.Timestamp)
			}
			return nil
		},
	}
	if imsConfig.UserAgent == "" {
		imsConfig.UserAgent = "VoHive/1"
	}
	if imsConfig.Transport == "" {
		imsConfig.Transport = "tcp"
	}

	aka := runtimeAKAProvider{sim: sim}
	provider, err := runtimeims.NewProvider(aka, imsConfig)
	if err != nil {
		return nil, nil, vowifimodel.IMSEvidence{}, false, err
	}
	imsIdentity := vowifimodel.SIMIdentity{
		IMSI: profile.IMSI,
		IMEI: profile.IMEI,
		HomeMCC: profile.MCC,
		HomeMNC: profile.MNC,
		SMSC: profile.SMSC,
		EPDG: strings.TrimSpace(prepared.EPDGAddr),
	}
	session, err := provider.Start(ctx, vowifimodel.IMSRequest{
		DeviceID: deviceID,
		Identity: imsIdentity,
		Tunnel:   imsTunnelEvidence{snapshot: tunnel.Snapshot()},
	})
	if err != nil {
		return nil, nil, vowifimodel.IMSEvidence{}, false, err
	}
	evidence := session.Evidence()
	if !evidence.Registered {
		_ = session.Close(context.Background())
		return nil, nil, evidence, false, errors.New("ims: SIP registration did not become ready")
	}
	smsReady := false
	if smsEvidence, smsErr := session.EnableSMS(ctx); smsErr == nil {
		smsReady = smsEvidence.Ready
	}
	service := &imsRuntimeService{
		session:  session,
		sender:   nil,
		deviceID: deviceID,
		imsi:     profile.IMSI,
		delivery: delivery,
	}
	if sender, ok := session.(vowifimodel.SMSSender); ok {
		service.sender = sender
	}
	if service.sender == nil && smsReady {
		_ = session.Close(context.Background())
		return nil, nil, evidence, false, errors.New("ims: registered session does not provide SMS sender")
	}
	return service, session, evidence, smsReady, nil
}

func configuredIMSSecurityMode() runtimeims.SecurityMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VOHIVE_VOWIFI_IMS_SECURITY"))) {
	case string(runtimeims.SecurityDisabled):
		return runtimeims.SecurityDisabled
	case string(runtimeims.SecurityOptional):
		return runtimeims.SecurityOptional
	default:
		return runtimeims.SecurityRequired
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func imsObs(evidence vowifimodel.IMSEvidence, smsReady bool) map[string]interface{} {
	return map[string]interface{}{
		"registered":            evidence.Registered,
		"registration_state":    evidence.RegistrationState,
		"associated_msisdn":     evidence.AssociatedMSISDN,
		"p_associated_uri":      append([]string(nil), evidence.PAssociatedURI...),
		"associated_identities": append([]string(nil), evidence.AssociatedIdentities...),
		"registered_contact":     evidence.RegisteredContact,
		"service_route":          append([]string(nil), evidence.ServiceRoute...),
		"transport":              evidence.Transport,
		"last_sip_code":          evidence.LastSIPCode,
		"security_mode":          evidence.SecurityMode,
		"security_verified":      evidence.SecurityVerified,
		"sms_ready":              smsReady,
	}
}

func monitorIMSRuntime(ctx context.Context, inst *Instance, session vowifimodel.IMSSession) {
	if inst == nil || session == nil {
		return
	}
	failureSource, ok := session.(vowifimodel.RuntimeFailureNotifier)
	if !ok {
		return
	}
	failures := failureSource.Failures()
	if failures == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-failures:
			if !ok || err == nil {
				return
			}
			state := inst.State()
			if state.Phase == PhaseStopped {
				return
			}
			state.Phase = PhaseFailed
			state.IMSReady = false
			state.SMSReady = false
			state.LastErrorClass = "ims"
			state.LastReason = "ims_runtime_failed"
			state.LastError = err.Error()
			state.UpdatedAt = time.Now()
			inst.setState(context.Background(), state)
		}
	}()
}
