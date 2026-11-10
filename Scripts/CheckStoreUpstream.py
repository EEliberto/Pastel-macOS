#!/usr/bin/env python3
"""Read-only by default. CI stages candidate sources in its disposable checkout."""
import argparse, io, json, os, pathlib, re, shutil, tarfile, tempfile, urllib.request
ROOT = pathlib.Path(__file__).resolve().parents[1]

def api(path):
    headers = {'Accept':'application/vnd.github+json','User-Agent':'Pastel-upstream-check'}
    if os.environ.get('GH_TOKEN'): headers['Authorization'] = 'Bearer ' + os.environ['GH_TOKEN']
    with urllib.request.urlopen(urllib.request.Request('https://api.github.com/'+path,headers=headers),timeout=30) as response:
        return json.load(response)

def stage(repository, revision):
    if not re.fullmatch(r'[0-9a-f]{40}',revision): raise ValueError('Invalid upstream revision')
    with urllib.request.urlopen(f'https://codeload.github.com/{repository}/tar.gz/{revision}',timeout=60) as response:
        archive = response.read(32*1024*1024+1)
    if len(archive)>32*1024*1024: raise ValueError('Upstream archive exceeds size limit')
    with tempfile.TemporaryDirectory(prefix='pastel-upstream-') as temporary:
        target=pathlib.Path(temporary)
        with tarfile.open(fileobj=io.BytesIO(archive),mode='r:gz') as tar:
            total=0
            for member in tar:
                parts=pathlib.PurePosixPath(member.name).parts[1:]
                if not parts or parts[0] not in {'pkg','internal','go.mod','go.sum','LICENSE'}: continue
                if '..' in parts or member.issym() or member.islnk(): raise ValueError('Unsafe archive entry')
                path=target.joinpath(*parts)
                if member.isdir(): path.mkdir(parents=True,exist_ok=True)
                elif member.isfile():
                    total+=member.size
                    if total>64*1024*1024: raise ValueError('Expanded source limit exceeded')
                    path.parent.mkdir(parents=True,exist_ok=True)
                    with tar.extractfile(member) as source, path.open('wb') as output: shutil.copyfileobj(source,output)
        destination=ROOT/'StoreHelper/ipatool'
        # Keep Pastel's small API extension; compile tests detect upstream breaks.
        adapter=(destination/'pkg/appstore/pastel_native.go').read_bytes()
        provenance=(destination/'PASTEL-PATCH.md').read_bytes()
        for name in ('pkg','internal','go.mod','go.sum','LICENSE'):
            source=target/name
            if not source.exists(): raise ValueError('Incomplete upstream source: '+name)
        # Update files in place: removing/replacing entire directories can
        # produce iCloud conflict copies in a developer's synced checkout.
        expected={p.relative_to(target) for p in target.rglob('*') if p.is_file()}
        for name in ('pkg','internal'):
            for existing in (destination/name).rglob('*'):
                if existing.is_file() and existing.relative_to(destination) not in expected:
                    existing.unlink()
        for relative in expected:
            output=destination/relative
            output.parent.mkdir(parents=True,exist_ok=True)
            shutil.copyfile(target/relative,output)
        (destination/'pkg/appstore/pastel_native.go').write_bytes(adapter)
        (destination/'PASTEL-PATCH.md').write_bytes(provenance)

def main():
    parser=argparse.ArgumentParser(); parser.add_argument('--stage',action='store_true'); args=parser.parse_args()
    pinned=json.loads((ROOT/'StoreHelper/upstream.json').read_text())
    repo=pinned['repository']
    if repo!='majd/ipatool': raise ValueError('Unexpected upstream repository')
    release=api(f'repos/{repo}/releases/latest')
    head=api(f'repos/{repo}/commits/main')
    revision=head['sha']
    changed=revision!=pinned.get('reviewedRevision',pinned['revision']) or release['tag_name']!=pinned['version']
    result={'changed':changed,'revision':revision,'release':release['tag_name'],
            'compare':f'https://github.com/{repo}/compare/{pinned["revision"]}...{revision}'}
    print(json.dumps(result,ensure_ascii=False))
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'],'a') as output:
            for key,value in result.items(): output.write(f'{key}={str(value).lower() if isinstance(value,bool) else value}\n')
    if args.stage and changed: stage(repo,revision)
if __name__=='__main__': main()
