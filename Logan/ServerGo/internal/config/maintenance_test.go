package config

import (
 "testing"
 "time"
)
func TestMaintenanceDefaultsAndValidation(t *testing.T){
 c,e:=Load(func(string)string{return ""});if e!=nil{t.Fatal(e)}
 if c.MaintenanceEnabled||c.MetricsEnabled||c.Maintenance.Retention!=7*24*time.Hour {t.Fatalf("unsafe defaults: %+v",c.Maintenance)}
 for _,values:=range []map[string]string{{"LOGAN_MAINTENANCE_ENABLED":"true"},{"LOGAN_MAINTENANCE_ENABLED":"oops"},{"LOGAN_METRICS_ENABLED":"oops"},{"LOGAN_RETENTION":"0s"},{"LOGAN_ORPHAN_AGE":"-1s"},{"LOGAN_MAINTENANCE_BATCH":"1001"},{"LOGAN_MAINTENANCE_TIMEOUT":"bad"}} {
  if _,e:=Load(func(k string)string{return values[k]});e==nil{t.Fatalf("accepted %v",values)}
 }
 c,e=Load(func(k string)string{return map[string]string{"LOGAN_METRICS_ENABLED":"true","LOGAN_RETENTION":"48h","LOGAN_MAINTENANCE_BATCH":"12"}[k]});if e!=nil||!c.MetricsEnabled||c.Maintenance.Retention!=48*time.Hour||c.Maintenance.Batch!=12{t.Fatalf("config rejected: %+v %v",c.Maintenance,e)}
}
