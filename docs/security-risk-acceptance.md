# Security risk acceptances

## React Router RSC advisory

`GHSA-qwww-vcr4-c8h2` affects React Router's React Server Components action
handling. Orako's dashboard is a browser-only Vite SPA and does not enable or
import the RSC APIs involved.

The production dependency audit allows only this advisory and fails for every
other npm advisory. Remove the exception as soon as an upstream release fixes
the RSC issue without reintroducing the earlier open-redirect vulnerabilities.
