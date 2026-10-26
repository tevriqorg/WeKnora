package handler

import (
	"context"
	"encoding/hex"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// SetAIBOXResourceCatalog attaches the already-provisioned catalog when
// assembling the existing WeKnora router, not a parallel AIBOX API server.
func (h *KnowledgeHandler) SetAIBOXResourceCatalog(c interfaces.ResourceCatalog) {
	h.aiboxResources = c
}

// UpsertDerivedKnowledge uses the existing tenant-scoped Knowledge lifecycle.
func (h *KnowledgeHandler) UpsertDerivedKnowledge(c *gin.Context) {
	_, kbID, tenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to ingest derived knowledge"))
		return
	}
	if h.aiboxResources == nil {
		_ = c.Error(errors.NewInternalServerError("AIBOX resource catalog unavailable"))
		return
	}
	// The envelope contains derived text and locators, never raw file bytes.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var envelope types.DerivedKnowledgeEnvelope
	if err := c.ShouldBindJSON(&envelope); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid derived envelope"))
		return
	}
	if err := envelope.NormalizeAndValidate(); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), tenantID)
	if envelope.SourceResourceRef != "" {
		stored, resolveErr := h.aiboxResources.Resolve(ctx, envelope.SourceResourceRef)
		if resolveErr != nil || stored == nil || stored.TenantID != tenantID ||
			stored.Ownership != types.ResourceOwnershipExternal {
			_ = c.Error(errors.NewBadRequestError("Unregistered, managed or cross-tenant source resource"))
			return
		}
		registered, scopeErr := aiboxSourceRegisteredForKB(ctx, h.aiboxResources, tenantID, kbID, envelope.SourceResourceRef)
		if scopeErr != nil {
			_ = c.Error(errors.NewInternalServerError("AIBOX source scope check unavailable"))
			return
		}
		if !registered {
			_ = c.Error(errors.NewBadRequestError("Source resource is not registered for target KB"))
			return
		}
	}
	saved, err := service.NewDerivedKnowledgeEnvelopeService(h.kgService).Upsert(ctx, kbID, &envelope)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("Derived knowledge handoff failed"))
		return
	}
	if envelope.SourceResourceRef != "" {
		if err := h.aiboxResources.Bind(ctx, envelope.SourceResourceRef, "knowledge", saved.ID, "source"); err != nil {
			_ = c.Error(errors.NewInternalServerError("Derived source binding failed"))
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": saved})
}

// aiboxSourceRegisteredForKB fails closed when the catalog cannot prove
// an explicit source registration for this exact tenant and knowledge base.
func aiboxSourceRegisteredForKB(
	ctx context.Context, catalog interfaces.ResourceCatalog, tenantID uint64, kbID, ref string,
) (bool, error) {
	lookup, ok := catalog.(interfaces.AIBOXKBSourceLookup)
	if !ok {
		return false, errors.NewInternalServerError("AIBOX KB scope lookup unavailable")
	}
	return lookup.IsAIBOXSourceRegisteredForKB(ctx, tenantID, kbID, ref)
}

// safeAIBOXSourceResourcePath accepts only relative clean paths beneath an
// administrator-configured local storage prefix. It is not a filesystem check.
func safeAIBOXSourceResourcePath(prefix, candidate string) bool {
	if !strings.HasPrefix(prefix, "local://") || !strings.HasSuffix(prefix, "/") ||
		!strings.HasPrefix(candidate, prefix) {
		return false
	}
	relative := strings.TrimPrefix(candidate, prefix)
	return relative != "" && relative != "." && relative != ".." && !strings.HasPrefix(relative, "../") &&
		!strings.HasPrefix(relative, "/") &&
		!strings.Contains(relative, "\\") && !strings.Contains(relative, "%") &&
		!strings.Contains(relative, "//") && !strings.ContainsRune(relative, '\x00') &&
		path.Clean(relative) == relative
}

// validAIBOXSourceSHA256 prevents a forged or malformed source fingerprint
// from being recorded as verified provenance. This validates syntax only:
// independent corpus verification still has to hash the actual source bytes.
func validAIBOXSourceSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// RegisterAIBOXExternalResource records an approved, remotely authoritative
// raw object without uploading or copying bytes into WeKnora storage.
func (h *KnowledgeHandler) RegisterAIBOXExternalResource(c *gin.Context) {
	_, kbID, tenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		_ = c.Error(errors.NewForbiddenError("No permission to register external source"))
		return
	}
	if h.aiboxResources == nil {
		_ = c.Error(errors.NewInternalServerError("AIBOX resource catalog unavailable"))
		return
	}
	prefix := os.Getenv("AIBOX_INGRESS_ALLOWED_RESOURCE_PREFIX")
	if prefix == "" {
		_ = c.Error(errors.NewForbiddenError("AIBOX external source registration disabled"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req struct {
		ResourcePath string `json:"resource_path" binding:"required"`
		Name         string `json:"name" binding:"required"`
		Mime         string `json:"mime"`
		SHA256       string `json:"sha256"`
		Size         int64  `json:"size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid external source descriptor"))
		return
	}
	if !safeAIBOXSourceResourcePath(prefix, req.ResourcePath) || req.Size < 0 || !validAIBOXSourceSHA256(req.SHA256) {
		_ = c.Error(errors.NewBadRequestError("External source is outside approved UAT root"))
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), tenantID)
	ref, err := h.aiboxResources.Register(ctx, tenantID, req.ResourcePath, interfaces.ResourceRegistration{
		Kind: "source", MimeType: req.Mime, OriginalName: req.Name, Size: req.Size,
		ContentHash: req.SHA256, Ownership: types.ResourceOwnershipExternal,
	})
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("External registration failed"))
		return
	}
	// Explicit KB scope: a resource in the same tenant is not automatically
	// a source for every KB. Retrying after a binding error is safe.
	lookup, ok := h.aiboxResources.(interfaces.AIBOXKBSourceLookup)
	if !ok {
		_ = c.Error(errors.NewInternalServerError("AIBOX source scope lookup unavailable"))
		return
	}
	registered, checkErr := lookup.IsAIBOXSourceRegisteredForKB(ctx, tenantID, kbID, ref)
	if checkErr != nil {
		_ = c.Error(errors.NewInternalServerError("External registration scope check failed"))
		return
	}
	if !registered {
		if err := h.aiboxResources.Bind(ctx, ref, types.ResourceOwnerKnowledgeBase, kbID, types.ResourceRelationAIBOXSource); err != nil {
			_ = c.Error(errors.NewInternalServerError("External source KB binding failed"))
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"resource_ref": ref, "ownership": "external"}})
}
