// A local Unix-socket HTTP stub. This is NOT the real fnOS music service.
package main
import (
 "encoding/json"
 "net"
 "net/http"
 "os"
)
func main(){
 if len(os.Args)<2{panic("expected Unix socket path")}
 l,e:=net.Listen("unix",os.Args[1]);if e!=nil{panic(e)}
 http.HandleFunc("/music/api/v1/user/me",func(w http.ResponseWriter,r *http.Request){w.WriteHeader(401)})
 http.HandleFunc("/music/api/v1/track/stream",func(w http.ResponseWriter,r *http.Request){
  if r.Header.Get("Range")!="" {w.Header().Set("Content-Range","bytes 0-3/4");w.WriteHeader(206)}
  _,_=w.Write([]byte("demo"))
 })
 http.HandleFunc("/music/api/v1/search/track",func(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"code":0,"data":map[string]any{"list":[]any{},"total":0}})})
 if e=http.Serve(l,nil);e!=nil{panic(e)}
}
