package handler

import (
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

// safeAIBOXSourceResourcePath accepts only relative clean paths beneath an
// administrator-configured local storage prefix. It is not a filesystem check.
func safeAIBOXSourceResourcePath(prefix, candidate string) bool {
	if !strings.HasPrefix(prefix, "local://") || !strings.HasSuffix(prefix, "/") ||
		!strings.HasPrefix(candidate, prefix) {
		return false
	}
	relative := strings.TrimPrefix(candidate, prefix)
	return relative != "" && !strings.HasPrefix(relative, "/") &&
		!strings.Contains(relative, "\\") && !strings.Contains(relative, "%") &&
		!strings.Contains(relative, "//") && !strings.ContainsRune(relative, '\x00') &&
		path.Clean(relative) == relative
}

// RegisterAIBOXExternalResource records an approved, remotely authoritative
// raw object without uploading or copying bytes into WeKnora storage.
func (h *KnowledgeHandler) RegisterAIBOXExternalResource(c *gin.Context) {
	_, _, tenantID, permission, err := h.validateKnowledgeBaseAccess(c)
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
	if !safeAIBOXSourceResourcePath(prefix, req.ResourcePath) || req.Size < 0 {
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
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"resource_ref": ref, "ownership": "external"}})
}
