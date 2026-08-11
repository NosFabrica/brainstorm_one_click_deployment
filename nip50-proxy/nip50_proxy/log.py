import logging
import sys

logging.basicConfig(
    level=logging.INFO,
    stream=sys.stdout,
    format="%(asctime)s %(levelname)s [nip50-proxy] %(message)s",
)

log = logging.getLogger("nip50-proxy")
