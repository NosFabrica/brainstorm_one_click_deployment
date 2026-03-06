to deploy, you need to first create 2 docker images.

clone the brainstorm_server repo, and on the main directory, run:

docker build -t brainstorm-server-service .

clone the brainstorm_graperank repo, and on the main directory, run:

docker build -t brainstorm-graperank-service .


now, on this repo's main directory, run:

docker compose up

if you want it to keep running, run:

docker compose up -d