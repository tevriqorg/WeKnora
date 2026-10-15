package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DerivedKnowledgeEnvelopeSchema = "aibox.derived-knowledge-envelope/v1"
	KnowledgeDerivedMetadataKey    = "_aibox_derived"
)

var allowedDerivedCapabilities = map[string]bool{
	"ocr": true, "caption": true, "asr": true, "keyframes": true,
	"timeline": true, "vlm": true, "metadata": true, "transcript": true,
}

type DerivedAssetRef struct {
	AssetID     string         `json:"asset_id"`
	ResourceRef string         `json:"resource_ref"`
	Kind        string         `json:"kind,omitempty"`
	SHA256      string         `json:"sha256,omitempty"`
	Locator     *SourceLocator `json:"locator,omitempty"`
}

type DerivedKnowledgeEnvelope struct {
	Schema                 string            `json:"schema"`
	SourceKey              string            `json:"source_key"`
	SourceRevisionID       string            `json:"source_revision_id"`
	DerivedObjectID        string            `json:"derived_object_id"`
	Title                  string            `json:"title"`
	Markdown               string            `json:"markdown,omitempty"`
	StructuredText         json.RawMessage   `json:"structured_text,omitempty"`
	Assets                 []DerivedAssetRef `json:"assets,omitempty"`
	Keyframes              []DerivedAssetRef `json:"keyframes,omitempty"`
	SourceResourceRef      string            `json:"source_resource_ref,omitempty"`
	SourceLocators         SourceLocators    `json:"source_locators,omitempty"`
	Processor              string            `json:"processor"`
	ProcessorVersion       string            `json:"processor_version"`
	ModelConfigFingerprint string            `json:"model_config_fingerprint,omitempty"`
	Capabilities           []string          `json:"capabilities,omitempty"`
	AnalysisManifestRef    string            `json:"analysis_manifest_ref,omitempty"`
	IdempotencyKey         string            `json:"idempotency_key"`
	ProducedAt             time.Time         `json:"produced_at"`
	AnalysisStatus         string            `json:"analysis_status"`
}

type DerivedKnowledgeMetadata struct {
	Schema                 string            `json:"schema"`
	SourceKey              string            `json:"source_key"`
	SourceRevisionID       string            `json:"source_revision_id"`
	DerivedObjectID        string            `json:"derived_object_id"`
	SourceResourceRef      string            `json:"source_resource_ref,omitempty"`
	SourceLocators         SourceLocators    `json:"source_locators,omitempty"`
	Assets                 []DerivedAssetRef `json:"assets,omitempty"`
	Keyframes              []DerivedAssetRef `json:"keyframes,omitempty"`
	Processor              string            `json:"processor"`
	ProcessorVersion       string            `json:"processor_version"`
	ModelConfigFingerprint string            `json:"model_config_fingerprint,omitempty"`
	Capabilities           []string          `json:"capabilities,omitempty"`
	AnalysisManifestRef    string            `json:"analysis_manifest_ref,omitempty"`
	IdempotencyKey         string            `json:"idempotency_key"`
	SnapshotHash           string            `json:"snapshot_hash"`
	ProducedAt             time.Time         `json:"produced_at"`
	AnalysisStatus         string            `json:"analysis_status"`
}

func DerivedKnowledgeIdempotencyKey(sourceRevisionID, derivedObjectID, processorVersion string) (string, error) {
	parts := []string{strings.TrimSpace(sourceRevisionID), strings.TrimSpace(derivedObjectID), strings.TrimSpace(processorVersion)}
	for _, part := range parts {
		if part == "" {
			return "", errors.New("derived identity components must be non-empty")
		}
	}
	raw := strings.Join(append([]string{"aibox-derived-v1"}, parts...), "\n")
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func DerivedKnowledgeID(tenantID uint64, kbID, idempotencyKey string) string {
	name := fmt.Sprintf("urn:tevriq:aibox:derived-knowledge:v1\ntenant=%d\nkb=%s\nkey=%s", tenantID, kbID, idempotencyKey)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)).String()
}

func (e *DerivedKnowledgeEnvelope) NormalizeAndValidate() error {
	if e == nil {
		return errors.New("derived envelope is required")
	}
	if e.Schema == "" {
		e.Schema = DerivedKnowledgeEnvelopeSchema
	}
	if e.Schema != DerivedKnowledgeEnvelopeSchema {
		return fmt.Errorf("unsupported derived envelope schema %q", e.Schema)
	}
	for name, value := range map[string]string{
		"source_key": e.SourceKey, "source_revision_id": e.SourceRevisionID,
		"derived_object_id": e.DerivedObjectID, "title": e.Title,
		"processor": e.Processor, "processor_version": e.ProcessorVersion,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if strings.TrimSpace(e.Markdown) == "" && len(e.StructuredText) == 0 {
		return errors.New("markdown or structured_text is required")
	}
	status := strings.ToLower(strings.TrimSpace(e.AnalysisStatus))
	if status == "" {
		status = "ready"
	}
	if status != "ready" && status != "pending" && status != "failed" {
		return errors.New("analysis_status must be ready, pending or failed")
	}
	e.AnalysisStatus = status
	caps := make(map[string]bool)
	for _, raw := range e.Capabilities {
		capability := strings.ToLower(strings.TrimSpace(raw))
		if capability == "" {
			continue
		}
		if !allowedDerivedCapabilities[capability] {
			return fmt.Errorf("unsupported derived capability %q", capability)
		}
		caps[capability] = true
	}
	e.Capabilities = e.Capabilities[:0]
	for capability := range caps {
		e.Capabilities = append(e.Capabilities, capability)
	}
	sort.Strings(e.Capabilities)
	for _, ref := range append(append([]DerivedAssetRef(nil), e.Assets...), e.Keyframes...) {
		if strings.TrimSpace(ref.AssetID) == "" || strings.TrimSpace(ref.ResourceRef) == "" {
			return errors.New("derived asset requires asset_id and resource_ref")
		}
		if temporaryDerivedRef(ref.ResourceRef) {
			return errors.New("temporary staging asset cannot become durable evidence")
		}
	}
	if temporaryDerivedRef(e.SourceResourceRef) {
		return errors.New("temporary staging source cannot become durable evidence")
	}
	expected, err := DerivedKnowledgeIdempotencyKey(e.SourceRevisionID, e.DerivedObjectID, e.ProcessorVersion)
	if err != nil {
		return err
	}
	if e.IdempotencyKey != "" && e.IdempotencyKey != expected {
		return errors.New("idempotency_key does not match derived logical input")
	}
	e.IdempotencyKey = expected
	if e.ProducedAt.IsZero() {
		e.ProducedAt = time.Now().UTC()
	}
	return nil
}

func temporaryDerivedRef(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	return strings.Contains(ref, ".part") || strings.Contains(ref, "/staging/")
}

func (e *DerivedKnowledgeEnvelope) Content() string {
	if strings.TrimSpace(e.Markdown) != "" {
		return strings.TrimSpace(e.Markdown)
	}
	var v any
	if json.Unmarshal(e.StructuredText, &v) == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			return "```json\n" + string(b) + "\n```"
		}
	}
	return string(e.StructuredText)
}

func (e *DerivedKnowledgeEnvelope) Metadata() (*DerivedKnowledgeMetadata, error) {
	if err := e.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return &DerivedKnowledgeMetadata{
		Schema: e.Schema, SourceKey: e.SourceKey, SourceRevisionID: e.SourceRevisionID,
		DerivedObjectID: e.DerivedObjectID, SourceResourceRef: e.SourceResourceRef,
		SourceLocators: e.SourceLocators, Assets: e.Assets, Keyframes: e.Keyframes,
		Processor: e.Processor, ProcessorVersion: e.ProcessorVersion,
		ModelConfigFingerprint: e.ModelConfigFingerprint, Capabilities: e.Capabilities,
		AnalysisManifestRef: e.AnalysisManifestRef, IdempotencyKey: e.IdempotencyKey,
		SnapshotHash: hex.EncodeToString(sum[:]), ProducedAt: e.ProducedAt, AnalysisStatus: e.AnalysisStatus,
	}, nil
}

func (k *Knowledge) DerivedMetadata() (*DerivedKnowledgeMetadata, error) {
	if k == nil || len(k.Metadata) == 0 {
		return nil, nil
	}
	fields, err := k.Metadata.Map()
	if err != nil {
		return nil, err
	}
	raw, ok := fields[KnowledgeDerivedMetadataKey]
	if !ok {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var meta DerivedKnowledgeMetadata
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (k *Knowledge) SetDerivedMetadata(meta *DerivedKnowledgeMetadata) error {
	if k == nil || meta == nil {
		return nil
	}
	fields, err := k.Metadata.Map()
	if err != nil {
		return err
	}
	fields[KnowledgeDerivedMetadataKey] = meta
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	k.Metadata = JSON(b)
	return nil
}
