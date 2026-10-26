"""Compile/package only. Does not run tests or access a speaker."""
from pathlib import Path
import hashlib,json,os,subprocess,zipfile
plugin=Path(__file__).resolve().parent
env=os.environ.copy();env.update(CGO_ENABLED='0',GOOS='linux',GOARCH='arm64')
subprocess.run(['go','build','-mod=readonly','-trimpath','-ldflags=-s -w','-o',str(plugin/'plugin.elf'),'./service'],cwd=plugin,env=env,check=True)
manifest=json.loads((plugin/'manifest.json').read_text(encoding='utf-8'))
manifest['module_sha256']=hashlib.sha256((plugin/'plugin.elf').read_bytes()).hexdigest()
(plugin/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
out=plugin.parents[1]/'release';out.mkdir(exist_ok=True)
target=out/f"ssh-{manifest['version']}.aliceplugin"
with zipfile.ZipFile(target,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as archive:
 for name in ['manifest.json','plugin.elf','README.md','LICENSE']:archive.write(plugin/name,name)
print(target.name,hashlib.sha256(target.read_bytes()).hexdigest())
