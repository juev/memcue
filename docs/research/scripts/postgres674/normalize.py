import json,subprocess
class Normalizer:
    def __init__(self,bridge): self.bridge=str(bridge)
    def query(self,q):
        return json.loads(subprocess.run([self.bridge,'normalize','query'],input=json.dumps(q),text=True,capture_output=True,check=True,timeout=5).stdout)
    def stems(self,text):
        return json.loads(subprocess.run([self.bridge,'normalize','stems'],input=text,text=True,capture_output=True,check=True,timeout=5).stdout)
