"""Build the bootstrap only. Does not include or publish proprietary firmware."""
from pathlib import Path
import hashlib,json,os,subprocess,zipfile
root=Path(__file__).resolve().parent
env=os.environ.copy();env.update(CGO_ENABLED='0',GOOS='linux',GOARCH='arm64')
subprocess.run(['go','build','-trimpath','-ldflags=-s -w','-o','plugin.elf','./service'],cwd=root,env=env,check=True)
manifest=json.loads((root/'manifest.json').read_text(encoding='utf-8'));manifest['module_sha256']=hashlib.sha256((root/'plugin.elf').read_bytes()).hexdigest()
(root/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
with zipfile.ZipFile(root/f"alice-legacy-fix-{manifest['version']}.aliceplugin",'w',zipfile.ZIP_DEFLATED) as z:
 for name in ['manifest.json','plugin.elf','README.md','LICENSE']:z.write(root/name,name)
