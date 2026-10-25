/* MIT License. Example Alice Homebrew ABI 1 module; no WASI or libc. */
typedef unsigned int u32;
typedef unsigned char u8;
__attribute__((import_module("alice"),import_name("config_number"))) extern double cfg_num(const char*,u32);
__attribute__((import_module("alice"),import_name("config_string"))) extern u32 cfg_str(const char*,u32,char*,u32);
__attribute__((import_module("alice"),import_name("led_write"))) extern u32 led_write(const u8*,u32);
static u8 frame[36];
static unsigned hex(char c){return c>='0'&&c<='9'?c-'0':c>='a'&&c<='f'?c-'a'+10:c>='A'&&c<='F'?c-'A'+10:0;}
static void hsv(unsigned hue,unsigned *r,unsigned *g,unsigned *b){
 unsigned s=(hue/256)%6,x=hue%256;
 switch(s){case 0:*r=255;*g=x;*b=0;break;case 1:*r=255-x;*g=255;*b=0;break;case 2:*r=0;*g=255;*b=x;break;case 3:*r=0;*g=255-x;*b=255;break;case 4:*r=x;*g=0;*b=255;break;default:*r=255;*g=0;*b=255-x;}
}
__attribute__((export_name("alice_tick"))) void alice_tick(u32 milliseconds){
 char color[8]={0},mode[16]={0};
 cfg_str("color",5,color,7);cfg_str("animation",9,mode,15);
 unsigned r=hex(color[1])*16+hex(color[2]),g=hex(color[3])*16+hex(color[4]),b=hex(color[5])*16+hex(color[6]);
 double brightness=cfg_num("brightness",10)/100.0;
 unsigned speed=(unsigned)cfg_num("speed",5),reverse=cfg_num("reverse",7)>0;
 unsigned phase=(milliseconds*(speed+5)/40)%1536;
 if(mode[0]=='b'){unsigned p=(milliseconds*(speed+5)/30)%2000;brightness*=0.08+0.92*(p<1000?p:2000-p)/1000.0;}
 for(unsigned i=0;i<12;i++){
  unsigned rr=r,gg=g,bb=b,index=reverse?11-i:i;
  if(mode[0]=='r')hsv((phase+index*128)%1536,&rr,&gg,&bb);
  double level=brightness;
  if(mode[0]=='c'){unsigned head=(phase/128)%12,distance=(index+12-head)%12;level*=distance==0?1:distance==1?0.25:distance==2?0.06:0;}
  frame[i*3]=(u8)(bb*level);frame[i*3+1]=(u8)(gg*level);frame[i*3+2]=(u8)(rr*level);
 }
 led_write(frame,36);
}
