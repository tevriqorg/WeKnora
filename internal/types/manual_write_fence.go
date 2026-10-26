package types

import (
 "context"
 "crypto/sha256"
 "encoding/json"
)
type manualWriteFenceKey struct{}
type ManualWriteFence struct { TenantID uint64; KnowledgeID, KBID string; MetadataHash [32]byte }
func canonicalManualMetadata(k *Knowledge) ([32]byte,error) {
 var value any
 if err:=json.Unmarshal(k.Metadata,&value);err!=nil{return [32]byte{},err}
 data,err:=json.Marshal(value);if err!=nil{return [32]byte{},err};return sha256.Sum256(data),nil
}
func WithManualWriteFence(ctx context.Context,k *Knowledge)(context.Context,error){
 hash,err:=canonicalManualMetadata(k);if err!=nil{return ctx,err}
 return context.WithValue(ctx,manualWriteFenceKey{},ManualWriteFence{k.TenantID,k.ID,k.KnowledgeBaseID,hash}),nil
}
func ManualWriteFenceFromContext(ctx context.Context)(ManualWriteFence,bool){f,ok:=ctx.Value(manualWriteFenceKey{}).(ManualWriteFence);return f,ok}
func(f ManualWriteFence) Matches(k *Knowledge) bool{
 if k==nil||k.ID!=f.KnowledgeID||k.TenantID!=f.TenantID||k.KnowledgeBaseID!=f.KBID{return false}
 hash,err:=canonicalManualMetadata(k);return err==nil&&hash==f.MetadataHash
}
