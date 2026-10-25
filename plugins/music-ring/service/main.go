//go:build musicring

// MIT, Alice Homebrew contributors.
package main

import (
 "encoding/base64"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "log"
 "math"
 "net"
 "os"
 "os/signal"
 "syscall"
 "time"
)

type config struct {
 Mode string `json:"mode"`
 Color string `json:"color"`
 Accent string `json:"accent"`
 Brightness float64 `json:"brightness"`
 Sensitivity float64 `json:"sensitivity"`
 Smoothing float64 `json:"smoothing"`
 Speed float64 `json:"speed"`
 Idle bool `json:"idle"`
}
type levels struct { Available bool `json:"available"`; RMS float64 `json:"rms"`; Peak float64 `json:"peak"`; Bands [12]float64 `json:"bands"` }
func clamp(x float64) float64 { return math.Max(0,math.Min(1,x)) }
func color(s string) ([]byte,error) {
 if len(s)!=7 || s[0]!='#' { return nil,fmt.Errorf("invalid color") }
 return hex.DecodeString(s[1:])
}
func run() error {
 raw,err:=os.ReadFile(os.Getenv("ALICE_CONFIG_PATH"));if err!=nil{return err}
 var c config;if err=json.Unmarshal(raw,&c);err!=nil{return err}
 first,err:=color(c.Color);if err!=nil{return err};second,err:=color(c.Accent);if err!=nil{return err}
 if c.Brightness<0||c.Brightness>100||c.Sensitivity<1||c.Sensitivity>60||c.Smoothing<0||c.Smoothing>95||c.Speed<1||c.Speed>100{return fmt.Errorf("invalid music ring settings")}
 if c.Mode!="spectrum"&&c.Mode!="pulse"&&c.Mode!="comet"{return fmt.Errorf("invalid animation")}
 conn,err:=net.Dial("udp4","127.0.0.1:18789");if err!=nil{return err};defer conn.Close()
 stop:=make(chan os.Signal,1);signal.Notify(stop,syscall.SIGTERM,os.Interrupt);defer signal.Stop(stop)
 ticker:=time.NewTicker(50*time.Millisecond);defer ticker.Stop()
 var smooth [12]float64;buffer:=make([]byte,2048);boot:=time.Now();lastLog:=time.Time{}
 log.Print("Music Ring ready. Needs Homebrew 0.5.0 and AirPlay 1.0.0-beta.2; microphone is not used.")
 defer fmt.Println("@alice-ring "+base64.StdEncoding.EncodeToString(make([]byte,36)))
 for {
  select{
  case <-stop:return nil
  case <-ticker.C:
   var audio levels
   conn.SetDeadline(time.Now().Add(80*time.Millisecond))
   if _,err=conn.Write([]byte("ALICE_LEVELS1"));err==nil { var n int;n,err=conn.Read(buffer);if err==nil{err=json.Unmarshal(buffer[:n],&audio)} }
   if err!=nil&&time.Since(lastLog)>15*time.Second {log.Print("Waiting for Homebrew 0.5.0 audio meter: ",err);lastLog=time.Now()}
   if err!=nil {audio=levels{}}
   frame:=make([]byte,36);phase:=time.Since(boot).Seconds()*c.Speed/20
   for i:=0;i<12;i++ {
    level:=0.0
    if audio.Available {
     switch c.Mode {
     case "spectrum":level=math.Sqrt(clamp(audio.Bands[i]*c.Sensitivity))
     case "pulse":level=math.Sqrt(clamp(audio.RMS*c.Sensitivity))
     case "comet":position:=math.Mod(phase*2,12);distance:=math.Abs(float64(i)-position);distance=math.Min(distance,12-distance);level=math.Exp(-distance*distance/2)*math.Sqrt(clamp(audio.RMS*c.Sensitivity))
     }
    }
    if c.Idle&&level<0.01 {level=0.025*(1+math.Sin(phase))}
    retain:=c.Smoothing/100
    smooth[i]=retain*smooth[i]+(1-retain)*level
    mixed:=clamp(smooth[i]);light:=mixed*c.Brightness/100
    for channel:=0;channel<3;channel++ {v:=(float64(first[channel])*(1-mixed)+float64(second[channel])*mixed)*light;frame[i*3+2-channel]=byte(math.Round(math.Max(0,math.Min(255,v))))}
   }
   fmt.Println("@alice-ring "+base64.StdEncoding.EncodeToString(frame))
  }
 }
}
func main(){if err:=run();err!=nil{log.Fatal(err)}}
