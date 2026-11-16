# homelab

- **CI:** en automático (push, pull request, etiquetas) solo corren las pruebas de Linux (`ubuntu-latest`). Las de macOS y Windows consumen muchos minutos de GitHub Actions: déjalas solo para ejecución manual (`workflow_dispatch`). No las vuelvas a poner en automático.
