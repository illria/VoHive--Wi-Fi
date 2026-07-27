package usim

type AKAResult struct {
	RES    []byte
	CK     []byte
	IK     []byte
	AUTS   []byte
	Reject bool
}

func (r AKAResult) Successful() bool {
	return len(r.RES) != 0 && len(r.CK) != 0 && len(r.IK) != 0
}

func (r AKAResult) SynchronizationFailed() bool {
	return len(r.AUTS) != 0
}

func (r AKAResult) AuthenticationRejected() bool {
	return r.Reject
}
