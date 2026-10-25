/* MIT, Alice Homebrew contributors. PCM stays in process; only levels leave. */
#include <stdint.h>
#include <math.h>
#include <string.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <fcntl.h>
#include <unistd.h>

static void alice_meter_pcm(const int32_t *pcm, int frames, int channels, int rate) {
 enum { N=1024, BANDS=12 };
 static double block[N], window[N], coeff[BANDS];
 static int used=0, initialized=0, fd=-1, blocks=0, previous_rate=0;
 static struct sockaddr_in destination;
 const double frequencies[BANDS]={80,120,180,280,430,650,1000,1500,2300,3500,5500,8500};
 if (!pcm || frames<1 || channels<1 || channels>8 || rate<16000) return;
 if (!initialized) {
  initialized=1;
  fd=socket(AF_INET,SOCK_DGRAM|SOCK_NONBLOCK|SOCK_CLOEXEC,0);
  memset(&destination,0,sizeof(destination));
  destination.sin_family=AF_INET; destination.sin_port=htons(18789);
  destination.sin_addr.s_addr=htonl(0x7f000001);
  for(int i=0;i<N;i++) window[i]=0.5-0.5*cos(2.0*M_PI*i/(N-1));
 }
 if(fd<0) return;
 if(previous_rate!=rate) {
  previous_rate=rate; used=0;
  for(int k=0;k<BANDS;k++) coeff[k]=2*cos(2*M_PI*frequencies[k]/rate);
 }
 for(int f=0;f<frames;f++) {
  double mono=0;
  for(int c=0;c<channels;c++) mono+=pcm[f*channels+c]/2147483648.0;
  block[used++]=mono/channels;
  if(used!=N) continue;
  used=0;
  /* At 48 kHz publish about 23 summaries/s; never block playback. */
  if((++blocks%2)!=0) continue;
  double squares=0,peak=0;
  for(int i=0;i<N;i++){squares+=block[i]*block[i];if(fabs(block[i])>peak)peak=fabs(block[i]);}
  float values[14]; values[0]=(float)fmin(1,sqrt(squares/N));values[1]=(float)fmin(1,peak);
  for(int k=0;k<BANDS;k++) {
   double a=0,b=0;
   for(int i=0;i<N;i++){double next=block[i]*window[i]+coeff[k]*a-b;b=a;a=next;}
   double energy=fmax(0,a*a+b*b-coeff[k]*a*b);
   values[k+2]=(float)fmin(1,4*sqrt(energy)/N);
  }
  /* Mini 1 is little-endian AArch64; the protocol explicitly uses LE float32. */
  unsigned char packet[64];memcpy(packet,"ALICEAU1",8);memcpy(packet+8,values,sizeof(values));
  sendto(fd,packet,sizeof(packet),MSG_DONTWAIT,(struct sockaddr*)&destination,sizeof(destination));
 }
}
