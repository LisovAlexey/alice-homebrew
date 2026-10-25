/* Alice Homebrew discovery bridge, MIT, 2026 Alice Homebrew contributors.
 * Replaces the external-command backend. Shairport computes its own RAOP TXT
 * records; a supervised Go process advertises them using maintained mDNS code.
 */
#include "common.h"
#include "mdns.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
int mdns_pid = 0;
static int registration(char *name, char *unused, int port, char **txt, char **secondary) {
  (void)unused; (void)secondary;
  const char *path = getenv("ALICE_MDNS_PATH");
  if (!path || !name || strchr(name, '\n') || strlen(name)>200) return -1;
  char temp[4096];
  if (snprintf(temp, sizeof(temp), "%s.tmp", path) >= (int)sizeof(temp)) return -1;
  FILE *f=fopen(temp,"w"); if (!f) return -1;
  fprintf(f,"%d\n%s\n%s\n",port,name,config.regtype);
  int count=0;
  while(txt && *txt) {
    if (++count>40 || strlen(*txt)>255 || strchr(*txt,'\n')) {fclose(f); unlink(temp); return -1;}
    fprintf(f,"%s\n",*txt++);
  }
  if (fclose(f)!=0 || rename(temp,path)!=0) {unlink(temp);return -1;}
  return 0;
}
static void unregister_service(void) {const char *p=getenv("ALICE_MDNS_PATH");if(p)unlink(p);}
mdns_backend mdns_external_avahi={.name="external-avahi",.mdns_register=registration,.mdns_unregister=unregister_service};
mdns_backend mdns_external_dns_sd={.name="external-dns-sd",.mdns_register=registration,.mdns_unregister=unregister_service};
