# Builds the docs site. Mount the repo at /docs:
#   docker run --rm -v "$PWD":/docs watchcat-docs build --strict
#   docker run --rm -p 8000:8000 -v "$PWD":/docs watchcat-docs serve -a 0.0.0.0:8000
FROM squidfunk/mkdocs-material:9.6.14
COPY entrypoint.sh /usr/local/bin/docs-entrypoint
ENTRYPOINT ["docs-entrypoint"]
CMD ["build", "--strict"]
