to deploy, you need to first create 2 docker images.

clone the brainstorm_server repo, and on the main directory, run:

docker build -t brainstorm-server-service .

clone the brainstorm_graperank repo, and on the main directory, run:

docker build -t brainstorm-graperank-service .

clone the BrainstormUI repo, and on the main directory, run:

docker build -t brainstorm-ui-service --build-arg VITE_API_URL=https://brainstormserver.nosfabrica.com .


now, on this repo's main directory, run:

docker compose up

if you want it to keep running, run:

docker compose up -d