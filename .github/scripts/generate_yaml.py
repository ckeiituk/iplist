import os
import glob
import json
import urllib.request
import sys
from concurrent.futures import ThreadPoolExecutor

DIST_DIR = "dist/yaml"
CONFIG_DIR = "config"

def fetch_url(url):
    try:
        if not url: return []
        print(f"Fetching external: {url}")
        # Default timeout 10s
        with urllib.request.urlopen(url, timeout=10) as response:
            return response.read().decode('utf-8').splitlines()
    except Exception as e:
        print(f"Error fetching {url}: {e}", file=sys.stderr)
        return []

def normalize_domain(line):
    line = line.strip()
    # Remove quotes if present
    line = line.replace('"', '')
    if not line or line.startswith('#'):
        return None
    # Logic: if no wildcard/special prefix and no path separator, assume domain -> add +.
    if not (line.startswith('*') or line.startswith('+.') or line.startswith('.')) and '/' not in line:
        return "+." + line
    return line

def _suffix_host(pattern):
    if pattern.startswith('+.'):
        return pattern[2:]
    if pattern.startswith('.'):
        return pattern[1:]
    return None

def prune_subdomains(domains):
    # Drop redundant subdomains when a broader domain-suffix entry exists (e.g. +.example.com -> +.www.example.com).
    suffix_hosts = set()
    for item in domains:
        host = _suffix_host(item)
        if host:
            suffix_hosts.add(host)

    if not suffix_hosts:
        return domains

    redundant_hosts = set()
    for host in suffix_hosts:
        parts = host.split('.')
        for i in range(1, len(parts)):
            parent = '.'.join(parts[i:])
            if parent in suffix_hosts:
                redundant_hosts.add(host)
                break

    if not redundant_hosts:
        return domains

    return {item for item in domains if _suffix_host(item) not in redundant_hosts}

def normalize_ip(line):
    line = line.strip()
    line = line.replace('"', '')
    if not line or line.startswith('#'):
        return None
    
    # If already has CIDR mask, return as-is
    if '/' in line:
        return line
    
    # Add CIDR mask for bare IPs
    if ':' in line:
        # IPv6 - add /128
        return f"{line}/128"
    else:
        # IPv4 - add /32
        return f"{line}/32"

def process_config(config_path):
    try:
        with open(config_path, 'r') as f:
            data = json.load(f)
    except Exception as e:
        print(f"Error reading {config_path}: {e}", file=sys.stderr)
        return None

    rel_path = os.path.relpath(config_path, CONFIG_DIR)
    group = os.path.dirname(rel_path)
    site = os.path.splitext(os.path.basename(rel_path))[0]
    
    if not group or not site:
        return None

    domains = data.get('domains', [])
    ip_list = []
    
    # Collect IPs/CIDRs
    ip_list.extend(data.get('ip4', []))
    ip_list.extend(data.get('ip6', []))
    ip_list.extend(data.get('cidr4', []))
    ip_list.extend(data.get('cidr6', []))
    
    # External processing
    ext = data.get('external', {})
    if ext:
        # We fetch simply here. For huge lists, could be async, but Python threads are fine for I/O.
        # Check specific external fields
        for field in ['domains']:
            for url in ext.get(field, []):
                domains.extend(fetch_url(url))
        for field in ['ip4', 'ip6', 'cidr4', 'cidr6']:
            for url in ext.get(field, []):
                ip_list.extend(fetch_url(url))

    # Normalize
    norm_domains = set()
    for d in domains:
        n = normalize_domain(str(d))
        if n: norm_domains.add(n)
    norm_domains = prune_subdomains(norm_domains)
        
    norm_ips = set()
    for i in ip_list:
        n = normalize_ip(str(i))
        if n: norm_ips.add(n)

    return {
        'group': group,
        'site': site,
        'domains': norm_domains,
        'ips': norm_ips
    }

def write_yaml(path, items):
    if not items: return
    sorted_items = sorted(items)
    
    # Build new content
    lines = ["payload:\n"]
    for item in sorted_items:
        lines.append(f'  - "{item}"\n')
    new_content = "".join(lines)
    
    # Only write if content differs (preserves timestamp for incremental MRS build)
    if os.path.exists(path):
        with open(path, "r") as f:
            existing = f.read()
        if existing == new_content:
            return  # No change, keep original timestamp
    
    with open(path, "w") as f:
        f.write(new_content)

def main():
    os.makedirs(f"{DIST_DIR}/domain", exist_ok=True)
    os.makedirs(f"{DIST_DIR}/ipcidr", exist_ok=True)
    
    files = glob.glob(f"{CONFIG_DIR}/**/*.json", recursive=True)
    
    # Parallel processing of configs (reads + fetching)
    results = []
    with ThreadPoolExecutor(max_workers=8) as executor:
        results = list(executor.map(process_config, files))
    
    # Aggregation
    groups_domains = {}
    groups_ips = {}
    all_domains = set()
    all_ips = set()
    
    for res in results:
        if not res: continue
        
        g = res['group']
        s = res['site']
        doms = res['domains']
        ips = res['ips']
        
        # Write Site YAML
        write_yaml(f"{DIST_DIR}/domain/{g}--{s}.yaml", doms)
        write_yaml(f"{DIST_DIR}/ipcidr/{g}--{s}.yaml", ips)
        
        # Aggregate Group
        if g not in groups_domains: groups_domains[g] = set()
        if g not in groups_ips: groups_ips[g] = set()
        
        groups_domains[g].update(doms)
        groups_ips[g].update(ips)
        
        # Aggregate All
        all_domains.update(doms)
        all_ips.update(ips)
        
    # Write Group YAMLs
    for g in groups_domains:
        write_yaml(f"{DIST_DIR}/domain/{g}.yaml", groups_domains[g])
    for g in groups_ips:
        write_yaml(f"{DIST_DIR}/ipcidr/{g}.yaml", groups_ips[g])
        
    # Write All YAMLs
    write_yaml(f"{DIST_DIR}/domain/all.yaml", all_domains)
    write_yaml(f"{DIST_DIR}/ipcidr/all.yaml", all_ips)
    
    print("YAML generation complete.")

if __name__ == "__main__":
    main()
