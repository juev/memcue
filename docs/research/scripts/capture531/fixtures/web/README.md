# Synthetic offline fixtures — do not fetch

Every page here is invented for #531 and was served to the agents by a local `web_fetch` stand-in, never fetched
from the network. The file names encode the URLs the scenarios use. `forum.queues.example` and `wiki.example.com`
are reserved names; `docs.brindle.dev`, `quarrel.io` and `ostrich.dev` are not, and may belong to real third
parties that have nothing to do with this content. Do not fetch these URLs, and do not read anything on them as the
source of these pages.
