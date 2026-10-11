FROM nginx:alpine
COPY deploy/docker/swagger/swagger.html /usr/share/nginx/html/
COPY deploy/docker/swagger/openapi.yaml /usr/share/nginx/html/
COPY deploy/docker/swagger/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
