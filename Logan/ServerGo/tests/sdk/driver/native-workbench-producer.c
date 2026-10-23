/* Public CLogan API: synthetic, non-production integration fixture. */
#include "clogan_core.h"
#include "clogan_status.h"
int main(int argc, char **argv) {
 if(argc != 2) return 2;
 int status=clogan_init(argv[1],argv[1],64*1024*1024,"0123456789012345","0123456789012345");
 if(status!=CLOGAN_INIT_SUCCESS_MMAP && status!=CLOGAN_INIT_SUCCESS_MEMORY) return 3;
 if(clogan_open("native.bin")!=CLOGAN_OPEN_SUCCESS) return 4;
 const char *messages[]={"TRIAD|001|LoginView: user tapped login; requestId=triad-login-1", "TRIAD|002|AuthClient: POST /login timed out; requestId=triad-login-1", "TRIAD|003|LoginViewModel: terminal state not observed; root cause unknown"};
 for(int i=0;i<3;i++) if(clogan_write(i+1,(char*)messages[i],1792402017341LL+i,"main",1,1)!=CLOGAN_WRITE_SUCCESS) return 5;
 return clogan_flush()==CLOGAN_FLUSH_SUCCESS?0:6;
}
