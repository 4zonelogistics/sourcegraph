import { writeFileSync } from 'fs'
import path from 'path'

import { sentryEsbuildPlugin } from '@sentry/esbuild-plugin'
import * as esbuild from 'esbuild'

import {
    MONACO_LANGUAGES_AND_FEATURES,
    ROOT_PATH,
    STATIC_ASSETS_PATH,
    stylePlugin,
    packageResolutionPlugin,
    monacoPlugin,
    RXJS_RESOLUTIONS,
    buildMonaco,
    buildTimerPlugin,
} from '@sourcegraph/build-config'
import { isDefined } from '@sourcegraph/common'

import { ENVIRONMENT_CONFIG, IS_DEVELOPMENT, IS_PRODUCTION } from '../utils'

import { manifestPlugin } from './manifestPlugin'

const isCodyApp = ENVIRONMENT_CONFIG.CODY_APP
const omitSlowDeps = ENVIRONMENT_CONFIG.DEV_WEB_BUILDER_OMIT_SLOW_DEPS

export const BUILD_OPTIONS: esbuild.BuildOptions = {
    entryPoints: {
        'scripts/app': isCodyApp
            ? path.join(ROOT_PATH, 'client/web/src/enterprise/app-main.tsx')
            : path.join(ROOT_PATH, 'client/web/src/enterprise/main.tsx'),
    },
    bundle: true,
    minify: IS_PRODUCTION,
    format: 'esm',
    logLevel: 'error',
    jsx: 'automatic',
    jsxDev: IS_DEVELOPMENT,
    splitting: true,
    chunkNames: 'chunks/chunk-[name]-[hash]',
    entryNames: IS_PRODUCTION ? 'scripts/[name]-[hash]' : undefined,
    outdir: STATIC_ASSETS_PATH,
    plugins: [
        stylePlugin,
        manifestPlugin,
        packageResolutionPlugin({
            path: require.resolve('path-browserify'),
            ...RXJS_RESOLUTIONS,
            ...(omitSlowDeps
                ? {
                      // Monaco
                      '@sourcegraph/shared/src/components/MonacoEditor':
                          '@sourcegraph/shared/src/components/NoMonacoEditor',
                      'monaco-editor': '/dev/null',
                      'monaco-editor/esm/vs/editor/editor.api': '/dev/null',
                      'monaco-yaml': '/dev/null',

                      // GraphiQL
                      './api/ApiConsole': path.join(ROOT_PATH, 'client/web/src/api/NoApiConsole.tsx'),
                      '@graphiql/react': '/dev/null',
                      graphiql: '/dev/null',

                      // Misc.
                      recharts: '/dev/null',
                  }
                : null),
        }),
        omitSlowDeps ? null : monacoPlugin(MONACO_LANGUAGES_AND_FEATURES),
        buildTimerPlugin,
        ENVIRONMENT_CONFIG.SENTRY_UPLOAD_SOURCE_MAPS
            ? sentryEsbuildPlugin({
                  org: ENVIRONMENT_CONFIG.SENTRY_ORGANIZATION,
                  project: ENVIRONMENT_CONFIG.SENTRY_PROJECT,
                  authToken: ENVIRONMENT_CONFIG.SENTRY_DOT_COM_AUTH_TOKEN,
                  silent: true,
                  release: { name: `frontend@${ENVIRONMENT_CONFIG.VERSION}` },
                  sourcemaps: { assets: path.join(ENVIRONMENT_CONFIG.STATIC_ASSETS_PATH, 'scripts', '*.map') },
              })
            : null,
    ].filter(isDefined),
    define: {
        ...Object.fromEntries(
            Object.entries({ ...ENVIRONMENT_CONFIG, SOURCEGRAPH_API_URL: undefined }).map(([key, value]) => [
                `process.env.${key}`,
                JSON.stringify(value === undefined ? null : value),
            ])
        ),
        global: 'window',
    },
    loader: {
        '.yaml': 'text',
        '.ttf': 'file',
        '.woff2': 'file',
        '.png': 'file',
    },
    target: 'esnext',
    sourcemap: true,
}

export const build = async (): Promise<void> => {
    const metafile = process.env.ESBUILD_METAFILE
    const result = await esbuild.build({
        ...BUILD_OPTIONS,
        outdir: STATIC_ASSETS_PATH,
        metafile: Boolean(metafile),
    })
    if (metafile) {
        writeFileSync(metafile, JSON.stringify(result.metafile), 'utf-8')
    }
    if (!omitSlowDeps) {
        const ctx = await buildMonaco(STATIC_ASSETS_PATH)
        await ctx.rebuild()
        await ctx.dispose()
    }
}

if (require.main === module) {
    build()
        .catch(error => console.error('Error:', error))
        .finally(() => process.exit(0))
}
