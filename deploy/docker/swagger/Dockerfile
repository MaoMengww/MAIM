FROM nginx:alpine
COPY docs/spec/swagger.html /usr/share/nginx/html/
COPY docs/spec/openapi.yaml /usr/share/nginx/html/
COPY deploy/docker/swagger/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
