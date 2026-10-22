package handler

import (
    "net/http"

    "github.com/Tencent/WeKnora/internal/application/service"
    "github.com/Tencent/WeKnora/internal/errors"
    "github.com/Tencent/WeKnora/internal/types"
    "github.com/gin-gonic/gin"
)

// UpsertDerivedKnowledge reuses WeKnora's existing Knowledge + SourceRevision
// lifecycle. It is behind the normal KB-scoped ingest API key and RBAC gates;
// no separate AIBOX HTTP server or alternate queue is introduced.
func (h *KnowledgeHandler) UpsertDerivedKnowledge(c *gin.Context) {
    _, kbID, tenantID, permission, err := h.validateKnowledgeBaseAccess(c)
    if err != nil { _ = c.Error(err); return }
    if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
        _ = c.Error(errors.NewForbiddenError("No permission to ingest derived knowledge"))
        return
    }
    // A DerivedKnowledgeEnvelope contains only derived text and locators, never
    // the raw file bytes. Reject unbounded inputs at the HTTP boundary.
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
    saved, err := service.NewDerivedKnowledgeEnvelopeService(h.kgService).Upsert(ctx, kbID, &envelope)
    if err != nil {
        _ = c.Error(errors.NewInternalServerError("Derived knowledge handoff failed"))
        return
    }
    c.JSON(http.StatusOK, gin.H{"success":true,"data":saved})
}
