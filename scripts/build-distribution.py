"""Build only Homebrew. No rooting tools, firmware or private data are packaged."""
from pathlib import Path
import hashlib
import json
import os
import subprocess
import zipfile

root=Path(__file__).resolve().parents[1]
out=root/'release';out.mkdir(exist_ok=True)
build=root/'bin';build.mkdir(exist_ok=True)
for system,arch,extension in [('windows','amd64','.exe'),('linux','amd64',''),('linux','arm64','')]:
    env=os.environ.copy();env.update(CGO_ENABLED='0',GOOS=system,GOARCH=arch)
    subprocess.run(['go','build','-trimpath','-ldflags=-s -w','-o',str(build/f'alice-homebrew-{system}-{arch}{extension}'),'./cmd/alice-homebrew'],cwd=root,env=env,check=True)
package=out/'AliceHomebrew-0.3.0.zip'
with zipfile.ZipFile(package,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
    for path in sorted(build.iterdir()):z.write(path,'bin/'+path.name)
    for name in ['README.md','LICENSE','THIRD_PARTY.md','VALIDATION.md','RELEASE_NOTES.md']:
        z.write(root/name,name)
    for directory in ['scripts','licenses','examples','sdk','docs']:
        for path in sorted((root/directory).rglob('*')):
            if path.is_file() and '__pycache__' not in path.parts:z.write(path,path.relative_to(root).as_posix())
    for path in (root/'docs/packages').glob('*.aliceplugin'):
        with zipfile.ZipFile(path) as plugin:manifest=json.loads(plugin.read('manifest.json'))
        z.write(path,f"plugins/{manifest['id']}-{manifest['version']}.aliceplugin")
files=[package]
for path in (root/'docs/packages').glob('*.aliceplugin'):
    with zipfile.ZipFile(path) as plugin:manifest=json.loads(plugin.read('manifest.json'))
    target=out/f"{manifest['id']}-{manifest['version']}.aliceplugin";target.write_bytes(path.read_bytes());files.append(target)
(out/'SHA256SUMS.txt').write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in files),encoding='ascii')
print('Built Windows AMD64, Linux AMD64 and speaker ARM64. No private or rooting files included.')
