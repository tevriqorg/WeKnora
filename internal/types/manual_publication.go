package types

// ManualPublicationCommittedError distinguishes a durable metadata publication
// whose queue submission failed from a publication that never committed.
// Derived ingress must retain the source revision when this error is returned.
type ManualPublicationCommittedError struct { Err error }
func (e *ManualPublicationCommittedError) Error() string { return e.Err.Error() }
func (e *ManualPublicationCommittedError) Unwrap() error { return e.Err }
