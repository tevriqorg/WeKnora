package repository
import (
 "context"
 "testing"
 "github.com/Tencent/WeKnora/internal/types"
 "github.com/stretchr/testify/require"
)
func TestDerivedManualOldWorkerCannotOverwritePublishedMetadata(t *testing.T){
 db:=setupKnowledgeTestDB(t);repo:=NewKnowledgeRepository(db)
 k:=&types.Knowledge{ID:"manual-fence",TenantID:1,KnowledgeBaseID:"kb",ParseStatus:types.ParseStatusPending}
 require.NoError(t,k.SetManualMetadata(types.NewManualKnowledgeMetadata("v1",types.ManualKnowledgeStatusPublish,1)))
 require.NoError(t,db.Exec("INSERT INTO knowledges(id,tenant_id,knowledge_base_id,parse_status,metadata) VALUES(?,?,?,?,?)",k.ID,k.TenantID,k.KnowledgeBaseID,k.ParseStatus,string(k.Metadata)).Error)
 stale,err:=repo.GetKnowledgeByID(context.Background(),1,k.ID);require.NoError(t,err)
 ctx,err:=types.WithManualWriteFence(context.Background(),stale);require.NoError(t,err)
 require.NoError(t,k.SetManualMetadata(types.NewManualKnowledgeMetadata("v2",types.ManualKnowledgeStatusPublish,2)))
 require.NoError(t,db.Model(&types.Knowledge{}).Where("id = ?",k.ID).Update("metadata",k.Metadata).Error)
 stale.ParseStatus=types.ParseStatusFailed
 require.Error(t,repo.UpdateKnowledge(ctx,stale),"old in-flight worker must not write after v2 publication")
 stored,err:=repo.GetKnowledgeByID(context.Background(),1,k.ID);require.NoError(t,err);meta,err:=stored.ManualMetadata();require.NoError(t,err);require.Equal(t,2,meta.Version);require.Equal(t,"v2",meta.Content)
}
