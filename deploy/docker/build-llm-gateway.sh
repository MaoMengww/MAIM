#!/bin/bash
cd /home/maomeng/project/AIM
docker build -f deploy/docker/Dockerfile --build-arg SERVICE=llm-gateway -t aim-llm-gateway:latest .
