
import ru.sbrf.devops.Globals
def getConfigPath() {return "$globals.DIR_SRC"}

def onDistrib(app, distr) {
    sh """
    echo "${app.name}"
    """

    withCredentials([string(credentialsId: 'sa-tuz-ci-pvv_bs64', variable: 'TOKEN')]) {
        sh """
            # -f: HTTP-ошибка (404/SSO-страница) должна уронить сборку сразу, а не попасть
            # в файл и сломать сравнение ниже; --max-time: зависшее соединение не должно
            # висеть вечно.
            curl -fsS --max-time 60 -H "Authorization: Basic ${TOKEN}" -o id_commit_distr.txt https://portal.works.prod.sbt/ssd/tools/nexus-cd/repository/sbt_prod/sbt_PROD/CI90000875_view/${app.distrArtifactId}/${app.distrVersion}/${app.distrArtifactId}-${app.distrVersion}-distrib.txt

            # Генерация файла с ID коммита
            git rev-parse HEAD > commit-id.txt

            # Сравниваем без хвостовых переводов строки: rev-parse пишет '\n', а раздача может
            # быть без него или с CRLF — cmp сравнивает байты и не должен решать исход сборки.
            if [ "\$(tr -d ' \r' < id_commit_distr.txt)" = "\$(git rev-parse HEAD)" ]; then
                echo "id_commit_distr.txt и commit-id.txt совпадают — продолжаем сборку."
            else
                echo "ОШИБКА: id_commit_distr.txt и commit-id.txt НЕ СОВПАДАЮТ!"
                echo "Различия:"
                diff "id_commit_distr.txt" "commit-id.txt"
                exit 1  # Завершаем с ошибкой → джоба падает
            fi
        """
    }
}
return wrapJenkinsfile(this)